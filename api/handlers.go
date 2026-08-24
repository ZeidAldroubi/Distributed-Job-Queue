package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"distributed-job-queue/shared"
	"github.com/docker/docker/client"
	"github.com/go-redis/redis/v9"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type App struct {
	redis       *redis.Client
	docker      *client.Client
	rateLimiter *RateLimiter
	samples     []string
}

type createJobRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (a *App) jobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.allow(w, r) {
		return
	}
	var req createJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = shared.TypeResizeImage
	}
	if req.Type != shared.TypeResizeImage || len(req.Payload) == 0 {
		http.Error(w, "expected resize_image payload", http.StatusBadRequest)
		return
	}
	job, err := a.enqueue(r.Context(), req.Type, req.Payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": job.ID})
}

func (a *App) jobByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	job, err := a.getJob(r.Context(), id)
	if errors.Is(err, redis.Nil) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *App) bulkTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.allow(w, r) {
		return
	}
	ids := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		payload, _ := json.Marshal(shared.ResizePayload{ImageURL: a.samples[i%len(a.samples)]})
		job, err := a.enqueue(r.Context(), shared.TypeResizeImage, payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ids = append(ids, job.ID)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_ids": ids})
}

func (a *App) stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	a.redis.ZRemRangeByScore(ctx, shared.CompletedZSet, "0", fmt.Sprint(now.Add(-1*time.Minute).UnixMilli()))
	pending, _ := a.redis.LLen(ctx, shared.PendingQueue).Result()
	processing, _ := a.redis.LLen(ctx, shared.ProcessingQueue).Result()
	dead, _ := a.redis.LLen(ctx, shared.DeadLetterQueue).Result()
	recent, _ := a.redis.ZCount(ctx, shared.CompletedZSet, fmt.Sprint(now.Add(-10*time.Second).UnixMilli()), fmt.Sprint(now.UnixMilli())).Result()

	counts := map[string]int64{}
	iter := a.redis.Scan(ctx, 0, "job:*", 0).Iterator()
	for iter.Next(ctx) {
		var job shared.Job
		if raw, err := a.redis.Get(ctx, iter.Val()).Result(); err == nil && json.Unmarshal([]byte(raw), &job) == nil {
			counts[job.Status]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"queue_depth":  pending,
		"processing":   processing,
		"dead":         dead,
		"status":       counts,
		"jobs_per_sec": float64(recent) / 10.0,
	})
}

func (a *App) workers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workers, err := a.listWorkers(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, workers)
	case http.MethodPost:
		var req createWorkerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		worker, err := a.addWorker(r.Context(), req.Name)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errInvalidWorkerName) || errors.Is(err, errWorkerExists) {
				status = http.StatusBadRequest
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, http.StatusCreated, worker)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) workerAction(w http.ResponseWriter, r *http.Request) {
	name, action, ok := parseWorkerAction(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !validWorkerName(name) {
		http.Error(w, errInvalidWorkerName.Error(), http.StatusBadRequest)
		return
	}

	var err error
	switch {
	case r.Method == http.MethodPost && action == "kill":
		err = a.killWorker(r.Context(), name)
	case r.Method == http.MethodPost && action == "relaunch":
		err = a.relaunchWorker(r.Context(), name)
	case r.Method == http.MethodDelete && action == "":
		err = a.deleteWorker(r.Context(), name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, redis.Nil) {
			status = http.StatusNotFound
		}
		if errors.Is(err, errWorkerNotKilled) {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) ws(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	pubsub := a.redis.Subscribe(r.Context(), shared.EventsChannel)
	defer pubsub.Close()
	for msg := range pubsub.Channel() {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
			return
		}
	}
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	name := "index.html"
	switch r.URL.Path {
	case "/", "/index.html":
		name = "index.html"
	case "/style.css":
		name = "style.css"
	case "/app.js":
		name = "app.js"
	default:
		http.NotFound(w, r)
		return
	}
	for _, base := range []string{"/app/dashboard", "../dashboard", "dashboard"} {
		path := filepath.Join(base, name)
		if _, err := os.Stat(path); err == nil {
			http.ServeFile(w, r, path)
			return
		}
	}
	http.Error(w, "dashboard assets not found", http.StatusInternalServerError)
}

func (a *App) sampleImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	img := image.NewRGBA(image.Rect(0, 0, 640, 420))
	for y := 0; y < 420; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{uint8(30 + x/8), uint8(80 + y/5), uint8(180 - x/12), 255})
		}
	}
	png.Encode(w, img)
}

func (a *App) enqueue(ctx context.Context, typ string, payload json.RawMessage) (*shared.Job, error) {
	now := time.Now().UTC()
	job := &shared.Job{
		ID:          uuid.NewString(),
		Type:        typ,
		Payload:     payload,
		Status:      shared.StatusQueued,
		MaxAttempts: 3,
		CreatedAt:   now,
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	pipe := a.redis.TxPipeline()
	pipe.Set(ctx, shared.JobKey(job.ID), raw, 0)
	pipe.LPush(ctx, shared.PendingQueue, job.ID)
	pipe.Publish(ctx, shared.EventsChannel, mustJSON(shared.Event{Type: "job_queued", JobID: job.ID, Status: job.Status, Time: now}))
	_, err = pipe.Exec(ctx)
	return job, err
}

func (a *App) getJob(ctx context.Context, id string) (*shared.Job, error) {
	raw, err := a.redis.Get(ctx, shared.JobKey(id)).Result()
	if err != nil {
		return nil, err
	}
	var job shared.Job
	return &job, json.Unmarshal([]byte(raw), &job)
}

func (a *App) allow(w http.ResponseWriter, r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	ok, err := a.rateLimiter.Allow(r.Context(), ip)
	if err != nil {
		http.Error(w, "rate limiter unavailable", http.StatusServiceUnavailable)
		return false
	}
	if !ok {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
