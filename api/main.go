package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/docker/docker/client"
	"github.com/go-redis/redis/v9"
)

func main() {
	ctx := context.Background()
	addr := env("REDIS_ADDR", "redis:6379")
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}

	app := &App{
		redis:       rdb,
		docker:      dockerClient,
		rateLimiter: NewRateLimiter(rdb, 20, 5),
		samples: []string{
			"http://api:8080/sample/image.png",
			"http://api:8080/sample/image.png?variant=medium",
			"http://api:8080/sample/image.png?variant=large",
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.dashboard)
	mux.HandleFunc("/app.js", app.dashboard)
	mux.HandleFunc("/style.css", app.dashboard)
	mux.HandleFunc("/sample/image.png", app.sampleImage)
	mux.HandleFunc("/jobs", app.jobs)
	mux.HandleFunc("/jobs/", app.jobByID)
	mux.HandleFunc("/jobs/bulk-test", app.bulkTest)
	mux.HandleFunc("/stats", app.stats)
	mux.HandleFunc("/workers", app.workers)
	mux.HandleFunc("/workers/", app.workerAction)
	mux.HandleFunc("/ws", app.ws)

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Println("api listening on :8080")
	log.Fatal(srv.ListenAndServe())
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
