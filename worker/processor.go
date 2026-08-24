package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"distributed-job-queue/shared"
	"github.com/disintegration/imaging"
	"github.com/go-redis/redis/v9"
)

type Processor struct {
	redis             *redis.Client
	workerID          string
	outputDir         string
	visibilityTimeout time.Duration
}

func (p *Processor) workLoop(ctx context.Context) {
	for {
		id, err := p.redis.BRPopLPush(ctx, shared.PendingQueue, shared.ProcessingQueue, 0).Result()
		if err != nil {
			log.Printf("queue pop: %v", err)
			time.Sleep(time.Second)
			continue
		}
		p.process(ctx, id)
	}
}

func (p *Processor) process(ctx context.Context, id string) {
	job, err := p.getJob(ctx, id)
	if err != nil {
		log.Printf("get job %s: %v", id, err)
		p.redis.LRem(ctx, shared.ProcessingQueue, 1, id)
		return
	}
	if job.Status == shared.StatusDone {
		p.redis.LRem(ctx, shared.ProcessingQueue, 1, id)
		return
	}
	now := time.Now().UTC()
	job.Status = shared.StatusProcessing
	job.StartedAt = &now
	job.WorkerID = p.workerID
	p.saveJob(ctx, job)
	p.publish(ctx, "job_processing", job, "")
	publishWorker(p.redis, ctx, p.workerID, "processing", id)

	err = p.resize(job)
	if err == nil {
		done := time.Now().UTC()
		job.Status = shared.StatusDone
		job.CompletedAt = &done
		p.saveJob(ctx, job)
		p.redis.LRem(ctx, shared.ProcessingQueue, 1, id)
		p.redis.ZAdd(ctx, shared.CompletedZSet, redis.Z{Score: float64(done.UnixMilli()), Member: fmt.Sprintf("%s:%d", id, done.UnixNano())})
		p.publish(ctx, "job_done", job, "")
		publishWorker(p.redis, ctx, p.workerID, "idle", "")
		return
	}

	job.Attempts++
	job.StartedAt = nil
	job.WorkerID = ""
	if job.Attempts < job.MaxAttempts {
		job.Status = shared.StatusQueued
		p.saveJob(ctx, job)
		pipe := p.redis.TxPipeline()
		pipe.LRem(ctx, shared.ProcessingQueue, 1, id)
		pipe.LPush(ctx, shared.PendingQueue, id)
		pipe.Exec(ctx)
		p.publish(ctx, "job_retried", job, err.Error())
	} else {
		dead := time.Now().UTC()
		job.Status = shared.StatusDead
		job.CompletedAt = &dead
		p.saveJob(ctx, job)
		pipe := p.redis.TxPipeline()
		pipe.LRem(ctx, shared.ProcessingQueue, 1, id)
		pipe.LPush(ctx, shared.DeadLetterQueue, id)
		pipe.Exec(ctx)
		p.publish(ctx, "job_dead", job, err.Error())
	}
	publishWorker(p.redis, ctx, p.workerID, "idle", "")
}

func (p *Processor) resize(job *shared.Job) error {
	var payload shared.ResizePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	img, format, err := loadImage(payload)
	if err != nil {
		return err
	}
	dir := filepath.Join(p.outputDir, job.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	sizes := map[string]int{"thumbnail": 150, "medium": 640, "large": 1200}
	for name, width := range sizes {
		dst := imaging.Resize(img, width, 0, imaging.Lanczos)
		ext := "jpg"
		if format == "png" {
			ext = "png"
		}
		out, err := os.Create(filepath.Join(dir, name+"."+ext))
		if err != nil {
			return err
		}
		if ext == "png" {
			err = png.Encode(out, dst)
		} else {
			err = jpeg.Encode(out, dst, &jpeg.Options{Quality: 85})
		}
		out.Close()
		if err != nil {
			return err
		}
	}
	if delay, _ := strconv.Atoi(os.Getenv("PROCESSING_DELAY_MS")); delay > 0 {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
	return nil
}

func loadImage(payload shared.ResizePayload) (image.Image, string, error) {
	var data []byte
	var err error
	if payload.ImageB64 != "" {
		clean := payload.ImageB64
		if idx := strings.Index(clean, ","); idx >= 0 {
			clean = clean[idx+1:]
		}
		data, err = base64.StdEncoding.DecodeString(clean)
	} else if payload.ImageURL != "" {
		client := &http.Client{Timeout: 15 * time.Second}
		resp, reqErr := client.Get(payload.ImageURL)
		if reqErr != nil {
			return nil, "", reqErr
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, "", fmt.Errorf("download failed: %s", resp.Status)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	} else {
		err = errors.New("missing image_url or image_base64")
	}
	if err != nil {
		return nil, "", err
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	return img, format, err
}

func (p *Processor) reapLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ids, err := p.redis.LRange(ctx, shared.ProcessingQueue, 0, -1).Result()
		if err != nil {
			continue
		}
		for _, id := range ids {
			job, err := p.getJob(ctx, id)
			if err != nil || job.Status != shared.StatusProcessing || job.StartedAt == nil {
				continue
			}
			if time.Since(*job.StartedAt) < p.visibilityTimeout {
				continue
			}
			job.Attempts++
			job.Status = shared.StatusQueued
			job.StartedAt = nil
			job.WorkerID = ""
			p.saveJob(ctx, job)
			if p.requeueIfProcessing(ctx, id) {
				p.publish(ctx, "job_recovered", job, "visibility timeout expired")
			}
		}
	}
}

func (p *Processor) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		p.redis.Set(ctx, shared.WorkerKey(p.workerID), "online", 10*time.Second)
		p.refreshRegistry(ctx)
		<-ticker.C
	}
}

func (p *Processor) refreshRegistry(ctx context.Context) {
	raw, err := p.redis.HGet(ctx, shared.WorkersRegistry, p.workerID).Result()
	if err != nil {
		return
	}
	var worker struct {
		Name        string    `json:"name"`
		ContainerID string    `json:"container_id"`
		Status      string    `json:"status"`
		CreatedAt   time.Time `json:"created_at"`
		LastSeen    time.Time `json:"last_seen"`
	}
	if json.Unmarshal([]byte(raw), &worker) != nil {
		return
	}
	worker.Status = "running"
	worker.LastSeen = time.Now().UTC()
	updated, err := json.Marshal(worker)
	if err == nil {
		p.redis.HSet(ctx, shared.WorkersRegistry, p.workerID, updated)
	}
}

func (p *Processor) getJob(ctx context.Context, id string) (*shared.Job, error) {
	raw, err := p.redis.Get(ctx, shared.JobKey(id)).Result()
	if err != nil {
		return nil, err
	}
	var job shared.Job
	return &job, json.Unmarshal([]byte(raw), &job)
}

func (p *Processor) saveJob(ctx context.Context, job *shared.Job) error {
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return p.redis.Set(ctx, shared.JobKey(job.ID), raw, 0).Err()
}

func (p *Processor) publish(ctx context.Context, typ string, job *shared.Job, msg string) {
	p.redis.Publish(ctx, shared.EventsChannel, mustJSON(shared.Event{
		Type:     typ,
		JobID:    job.ID,
		WorkerID: job.WorkerID,
		Status:   job.Status,
		Message:  msg,
		Time:     time.Now().UTC(),
	}))
}

var requeueScript = redis.NewScript(`
if redis.call("LREM", KEYS[1], 1, ARGV[1]) > 0 then
  redis.call("LPUSH", KEYS[2], ARGV[1])
  return 1
end
return 0
`)

func (p *Processor) requeueIfProcessing(ctx context.Context, id string) bool {
	ok, err := requeueScript.Run(ctx, p.redis, []string{shared.ProcessingQueue, shared.PendingQueue}, id).Int()
	return err == nil && ok == 1
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
