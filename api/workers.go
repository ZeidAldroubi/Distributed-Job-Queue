package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"distributed-job-queue/shared"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/go-redis/redis/v9"
)

var (
	errInvalidWorkerName = errors.New("worker name must contain only letters, numbers, and hyphens")
	errWorkerExists      = errors.New("worker already exists")
	errWorkerNotKilled   = errors.New("worker must be killed before it can be relaunched")
	workerNamePattern    = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)
)

type createWorkerRequest struct {
	Name string `json:"name"`
}

type WorkerRecord struct {
	Name        string    `json:"name"`
	ContainerID string    `json:"container_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeen    time.Time `json:"last_seen"`
	CurrentJob  string    `json:"current_job,omitempty"`
}

func (a *App) addWorker(ctx context.Context, name string) (*WorkerRecord, error) {
	name = strings.TrimSpace(name)
	if !validWorkerName(name) {
		return nil, errInvalidWorkerName
	}
	if _, err := a.getWorker(ctx, name); err == nil {
		return nil, errWorkerExists
	} else if !errors.Is(err, redis.Nil) {
		return nil, err
	}

	networkName, outputMount := a.workerContainerRuntime(ctx)
	containerName := "job-queue-" + name
	image := env("WORKER_IMAGE", "job-queue-worker:latest")
	redisAddr := env("WORKER_REDIS_ADDR", env("REDIS_ADDR", "redis:6379"))
	delay := env("WORKER_PROCESSING_DELAY_MS", "1500")

	resp, err := a.docker.ContainerCreate(ctx,
		&container.Config{
			Image: image,
			Env: []string{
				"REDIS_ADDR=" + redisAddr,
				"WORKER_NAME=" + name,
				"OUTPUT_DIR=/output",
				"PROCESSING_DELAY_MS=" + delay,
			},
			Labels: map[string]string{
				"distributed-job-queue.worker": name,
			},
		},
		&container.HostConfig{Mounts: []mount.Mount{outputMount}},
		&network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: &network.EndpointSettings{},
		}},
		nil,
		containerName,
	)
	if err != nil {
		return nil, fmt.Errorf("create worker container: %w", err)
	}
	if err := a.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = a.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("start worker container: %w", err)
	}

	now := time.Now().UTC()
	worker := &WorkerRecord{
		Name:        name,
		ContainerID: resp.ID,
		Status:      "running",
		CreatedAt:   now,
		LastSeen:    now,
	}
	if err := a.saveWorker(ctx, worker); err != nil {
		_ = a.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, err
	}
	a.publishWorkerEvent(ctx, "worker_added", worker)
	return worker, nil
}

func (a *App) killWorker(ctx context.Context, name string) error {
	worker, err := a.getWorker(ctx, name)
	if err != nil {
		return err
	}
	timeout := 10
	if err := a.docker.ContainerStop(ctx, worker.ContainerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop worker container: %w", err)
	}
	worker.Status = "killed"
	worker.LastSeen = time.Now().UTC()
	if err := a.saveWorker(ctx, worker); err != nil {
		return err
	}
	a.publishWorkerEvent(ctx, "worker_killed", worker)
	return nil
}

func (a *App) relaunchWorker(ctx context.Context, name string) error {
	worker, err := a.getWorker(ctx, name)
	if err != nil {
		return err
	}
	if worker.Status != "killed" {
		return errWorkerNotKilled
	}
	if err := a.docker.ContainerStart(ctx, worker.ContainerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start worker container: %w", err)
	}
	worker.Status = "running"
	worker.LastSeen = time.Now().UTC()
	if err := a.saveWorker(ctx, worker); err != nil {
		return err
	}
	a.publishWorkerEvent(ctx, "worker_relaunched", worker)
	return nil
}

func (a *App) deleteWorker(ctx context.Context, name string) error {
	worker, err := a.getWorker(ctx, name)
	if err != nil {
		return err
	}
	if err := a.docker.ContainerRemove(ctx, worker.ContainerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove worker container: %w", err)
	}
	if err := a.redis.HDel(ctx, shared.WorkersRegistry, name).Err(); err != nil {
		return err
	}
	a.publishWorkerEvent(ctx, "worker_deleted", worker)
	return nil
}

func (a *App) listWorkers(ctx context.Context) ([]WorkerRecord, error) {
	raw, err := a.redis.HGetAll(ctx, shared.WorkersRegistry).Result()
	if err != nil {
		return nil, err
	}
	currentJobs := a.currentJobsByWorker(ctx)
	workers := make([]WorkerRecord, 0, len(raw))
	for _, value := range raw {
		var worker WorkerRecord
		if json.Unmarshal([]byte(value), &worker) == nil && worker.Status != "deleted" {
			worker.CurrentJob = currentJobs[worker.Name]
			workers = append(workers, worker)
		}
	}
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].Name < workers[j].Name
	})
	return workers, nil
}

func (a *App) currentJobsByWorker(ctx context.Context) map[string]string {
	current := map[string]string{}
	ids, err := a.redis.LRange(ctx, shared.ProcessingQueue, 0, -1).Result()
	if err != nil {
		return current
	}
	for _, id := range ids {
		job, err := a.getJob(ctx, id)
		if err == nil && job.WorkerID != "" {
			current[job.WorkerID] = id
		}
	}
	return current
}

func (a *App) getWorker(ctx context.Context, name string) (*WorkerRecord, error) {
	raw, err := a.redis.HGet(ctx, shared.WorkersRegistry, name).Result()
	if err != nil {
		return nil, err
	}
	var worker WorkerRecord
	return &worker, json.Unmarshal([]byte(raw), &worker)
}

func (a *App) saveWorker(ctx context.Context, worker *WorkerRecord) error {
	raw, err := json.Marshal(worker)
	if err != nil {
		return err
	}
	return a.redis.HSet(ctx, shared.WorkersRegistry, worker.Name, raw).Err()
}

func (a *App) publishWorkerEvent(ctx context.Context, typ string, worker *WorkerRecord) {
	a.redis.Publish(ctx, shared.EventsChannel, mustJSON(shared.Event{
		Type:     typ,
		WorkerID: worker.Name,
		Status:   worker.Status,
		Time:     time.Now().UTC(),
	}))
}

func (a *App) workerContainerRuntime(ctx context.Context) (string, mount.Mount) {
	networkName := env("WORKER_NETWORK", "distributed-job-queue_default")
	outputMount := mount.Mount{Type: mount.TypeVolume, Source: "job-queue-output", Target: "/output"}

	hostname, err := os.Hostname()
	if err != nil {
		return networkName, outputMount
	}
	inspect, err := a.docker.ContainerInspect(ctx, hostname)
	if err != nil {
		return networkName, outputMount
	}
	for name := range inspect.NetworkSettings.Networks {
		networkName = name
		break
	}
	for _, existing := range inspect.Mounts {
		if existing.Destination == "/output" {
			outputMount = mount.Mount{
				Type:   mount.Type(existing.Type),
				Source: existing.Source,
				Target: "/output",
			}
			break
		}
	}
	return networkName, outputMount
}

func validWorkerName(name string) bool {
	return workerNamePattern.MatchString(name)
}

func parseWorkerAction(path string) (string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/workers/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		return parts[0], "", true
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}
