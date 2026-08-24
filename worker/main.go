package main

import (
	"context"
	"log"
	"os"
	"time"

	"distributed-job-queue/shared"
	"github.com/go-redis/redis/v9"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: env("REDIS_ADDR", "redis:6379")})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	workerID := env("WORKER_NAME", env("WORKER_ID", "worker-"+uuid.NewString()[:8]))
	outputDir := env("OUTPUT_DIR", "/output")
	processor := &Processor{
		redis:             rdb,
		workerID:          workerID,
		outputDir:         outputDir,
		visibilityTimeout: 30 * time.Second,
	}
	go processor.reapLoop(ctx)
	go processor.heartbeatLoop(ctx)
	log.Printf("%s online", workerID)
	processor.workLoop(ctx)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func publishWorker(rdb *redis.Client, ctx context.Context, workerID, status, jobID string) {
	rdb.Publish(ctx, shared.EventsChannel, mustJSON(shared.Event{
		Type:     "worker_status",
		WorkerID: workerID,
		JobID:    jobID,
		Status:   status,
		Time:     time.Now().UTC(),
	}))
}
