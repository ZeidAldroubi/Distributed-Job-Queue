# Distributed Job Queue

A Redis-backed job queue with dynamic, on-demand workers. Built to practice the patterns behind real queue systems (SQS, Sidekiq, Celery): at-least-once delivery, retries, dead-lettering, rate limiting, crash recovery, and horizontal scaling — all controllable and observable from a live dashboard.

Jobs submitted through the API are picked up by independent worker containers that resize images. Workers can be added, killed, relaunched, or deleted at runtime, and if a worker dies mid-job, another worker automatically picks the job back up.

## Demo

Open the dashboard at `http://localhost:8080` after running the project. It shows live queue depth, worker status, and a scrolling event feed, and lets you:

- Submit test jobs and watch them move through queued → processing → done
- Add a new named worker
- Kill a specific worker (its in-progress job gets recovered by another worker)
- Relaunch a killed worker
- Delete a worker permanently

<img width="1895" height="852" alt="Screenshot 2026-08-23 220805" src="https://github.com/user-attachments/assets/5c1bd967-b872-41b9-8855-e0d483896199" />


## Architecture

```
Client / Dashboard
      |
      v
  Go API Server -----> Docker Engine (via socket)
      |                        |
      v                        v
    Redis                Worker containers
 (queue + registry)      (created on demand)
      |                        |
      +------ Pub/Sub ---------+
                |
                v
        WebSocket -> Dashboard
```

Job flow:
1. API receives a job, writes it to Redis, pushes its ID onto `queue:pending`
2. A worker atomically claims it (`queue:pending` -> `queue:processing`)
3. Worker resizes the image into thumbnail/medium/large versions
4. Worker marks it `done` and publishes an event
5. If a worker dies mid-job, a background reaper notices after 30 seconds and requeues the job for another worker
6. Jobs that fail are retried up to 3 times, then moved to `queue:dead_letter`

## Features

**Fault-tolerant processing** — jobs aren't lost if a worker crashes. A visibility-timeout reaper detects stale in-progress jobs and requeues them.

**Dynamic worker fleet** — workers aren't hardcoded in docker-compose. The API holds a Docker Engine connection and creates/stops/starts/removes worker containers on demand. Each worker has a name and a tracked status (running, killed, deleted) in a Redis registry.

**Idempotent execution** — delivery is at-least-once, not exactly-once, so a job could in theory run twice. Workers check job status before processing and use deterministic output paths, so re-runs are safe.

**Rate limiting** — a Redis-backed token bucket caps each client IP at 5 req/sec with some burst allowance.

**Live updates** — every state change (job or worker) is published over Redis Pub/Sub and forwarded to the dashboard over WebSocket, so nothing is polled.

## Tech stack

- Go — API server and workers
- Redis — queue, worker registry, pub/sub
- Docker Engine SDK (Go) + Docker Compose — container orchestration
- Vanilla HTML/CSS/JS — dashboard, no framework or build step

## Getting started

Requires Docker Desktop running locally (see `SETUP.md` if you don't have it installed).

1. Clone the repo and start the API + Redis:

```sh
git clone <your-repo-url>
cd distributed-job-queue
docker-compose up --build
```

2. Open a second terminal (leave the first one running) and build the worker image:

```sh
docker-compose build worker-image
```

3. Open any browser and go to:

```
http://localhost:8080
```

Use the dashboard's "Add Worker" button to bring workers online, then submit jobs.

## API

| Endpoint | Description |
|---|---|
| `POST /jobs` | Submit a new image-resize job |
| `GET /jobs/:id` | Check a job's status |
| `GET /stats` | Queue depth and throughput |
| `POST /jobs/bulk-test` | Submit 100 sample jobs |
| `GET /workers` | List all workers and status |
| `POST /workers` | Create a named worker |
| `POST /workers/:name/kill` | Stop a worker (recoverable) |
| `POST /workers/:name/relaunch` | Resume a killed worker |
| `DELETE /workers/:name` | Permanently remove a worker |
| `GET /ws` | WebSocket stream of live events |

## Design notes

**At-least-once instead of exactly-once delivery.** Exactly-once delivery isn't really achievable over a network in practice, so this follows the same approach real queue systems use: at-least-once delivery plus idempotent processing on the worker side.

**Why the API controls Docker directly.** The API container mounts `/var/run/docker.sock` so it can manage sibling worker containers, which is what makes on-demand scaling from the dashboard possible. Worth calling out explicitly: anything with access to the Docker socket has effective control over the host's Docker daemon. Fine for a local demo, but it would need a scoped orchestration layer instead of raw socket access before running with untrusted input.

**Token-bucket rate limiting** was chosen over a fixed window because it allows normal bursty traffic while still bounding sustained load.

## Possible next steps

- Persistent job history (currently Redis only, no long-term storage)
- Auth on the worker-management endpoints
- Horizontally scale the API itself behind a load balancer
eus
