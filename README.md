# ⚡ Distributed Job Queue

A production-pattern distributed job processing system — built from scratch to demonstrate the coordination, fault-tolerance, and scaling techniques behind systems like AWS SQS, Sidekiq, and Celery.

Submit image-processing jobs through a REST API, watch them get picked up by a fleet of independent workers, kill a worker mid-job and watch another one recover it automatically, and dynamically scale the worker fleet up or down — all live, on a real-time dashboard.

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-7-DC382D?logo=redis&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)
![WebSocket](https://img.shields.io/badge/Live-WebSocket-black)

---

## Why this exists

Most student projects are CRUD apps with a database. This one is different — it's a systems project. It answers the question every backend engineer eventually has to solve: **how do you reliably process work across multiple machines when any one of them can fail at any moment?**

This project implements, from first principles (no message broker library, no managed queue service):

- **At-least-once delivery** with idempotent job execution
- **Horizontal worker scaling** — add or remove workers on demand, at runtime
- **Automatic crash recovery** via a visibility-timeout reaper
- **Retry logic with a dead-letter queue** for jobs that can't be processed
- **Per-client rate limiting** using a Redis-backed token bucket
- **Live system observability** via Redis Pub/Sub → WebSocket → real-time dashboard
- **Dynamic container lifecycle management** — the API controls Docker itself to spin workers up and down

## Demo

Open the dashboard and you can:

| Action | What happens |
|---|---|
| **Submit jobs** | Watch them flow through `queued → processing → done` live |
| **Add Worker** | Type a name, a brand-new container spins up and starts pulling jobs |
| **Kill Worker** | Stop a specific worker mid-job — its in-flight job gets automatically recovered by another worker within the visibility timeout |
| **Relaunch Worker** | Bring a killed worker back to life, same identity, same container |
| **Delete Worker** | Permanently remove a worker from the fleet |

*Screenshot/GIF of the live dashboard goes here.*

## Architecture

```
Client / Dashboard
       │
       ▼
   Go API Server ───────► Docker Engine (via socket)
       │                        │
       ▼                        ▼
   Redis (queue +          Dynamically created
   worker registry)         named workers
       │                        │
       └──────── Pub/Sub ───────┘
                   │
                   ▼
         WebSocket → Dashboard (live updates)
```

See [`docs/architecture.md`](docs/architecture.md) for the full diagram source.

**Flow of a job:**
1. Client `POST`s a job to the API → API writes it to Redis and pushes its ID onto `queue:pending`
2. An available worker atomically claims the job (`queue:pending → queue:processing`)
3. Worker resizes the image into thumbnail/medium/large outputs
4. Worker marks the job `done` and publishes an event
5. If the worker dies mid-job, a background reaper notices the stale timestamp after 30 seconds and returns the job to `queue:pending` for another worker to claim
6. If a job fails outright, it retries up to 3 times before landing in `queue:dead_letter`

## Features in depth

### Fault-tolerant job processing
Jobs are never lost. If a worker crashes, is killed, or is deleted mid-job, a visibility-timeout reaper detects the stale job and requeues it for another worker — the same self-healing pattern used by real message queue systems.

### Dynamic worker fleet
Workers aren't hardcoded in `docker-compose.yml`. The API holds a live Docker Engine connection (via the mounted Docker socket) and can create, stop, start, and destroy worker containers on demand. Each worker has a user-chosen name and a tracked lifecycle state (`running`, `killed`, `deleted`) stored in a Redis-backed registry.

### Idempotent execution
Because delivery is at-least-once (not exactly-once — a deliberate, honest engineering tradeoff), a job could theoretically be picked up twice. Workers check job status before executing, and output paths are deterministic by job ID, so re-processing is always safe.

### Rate limiting
A Redis-backed token-bucket limiter caps each client IP at 5 requests/second with short burst allowance, protecting the queue from being flooded by a single client.

### Real-time observability
Every state transition — job queued, claimed, completed, retried, dead-lettered, and every worker added, killed, relaunched, or deleted — is published to a Redis Pub/Sub channel and forwarded over WebSocket to connected dashboard clients. No polling; the dashboard reflects backend state the instant it changes.

## Tech stack

| Layer | Technology |
|---|---|
| API & workers | Go |
| Queue, coordination, worker registry | Redis |
| Container orchestration | Docker Engine SDK (Go) + Docker Compose |
| Live updates | Redis Pub/Sub + WebSocket |
| Dashboard | Vanilla HTML/CSS/JS (no framework, no build step) |

## Getting started

Requires **Docker Desktop** running locally. See [`SETUP.md`](SETUP.md) for install instructions if you don't have it.

```sh
git clone <your-repo-url>
cd distributed-job-queue
docker-compose up --build
```

Build the worker image once so the API can spin up new workers on demand:

```sh
docker-compose build worker-image
```

Then open:

```
http://localhost:8080
```

Workers are no longer started as static services — use the dashboard's **Add Worker** control to bring your first workers online, then submit jobs and watch them process.

## API reference

| Endpoint | Description |
|---|---|
| `POST /jobs` | Submit a new image-resize job |
| `GET /jobs/:id` | Check a job's status |
| `GET /stats` | Current queue depth and throughput |
| `POST /jobs/bulk-test` | Submit 100 sample jobs at once |
| `GET /workers` | List all workers and their status |
| `POST /workers` | Create a new named worker |
| `POST /workers/:name/kill` | Stop a worker (recoverable) |
| `POST /workers/:name/relaunch` | Resume a killed worker |
| `DELETE /workers/:name` | Permanently remove a worker |
| `GET /ws` | WebSocket stream of live system events |

## Design decisions

**Why at-least-once instead of exactly-once delivery?** Exactly-once delivery across a network is, practically, not guaranteeable — the honest engineering answer is at-least-once delivery combined with idempotent processing, which is what real systems like SQS actually do. This project embraces that rather than pretending otherwise.

**Why does the API control Docker directly?** Mounting `/var/run/docker.sock` into the API container lets it manage sibling worker containers programmatically, enabling true on-demand scaling from the dashboard instead of a fixed worker count. This is a well-known pattern, but it's also a real trust boundary worth naming explicitly: anything with access to the Docker socket has effective administrative control over the host's Docker daemon. This is appropriate for a local demo; it would need stronger isolation (e.g., a scoped orchestration API, not raw socket access) before ever running with untrusted input in production.

**Why a token-bucket rate limiter?** It allows short, natural bursts of legitimate traffic while still bounding sustained load — a better fit for real client behavior than a strict fixed-window limiter.

## Load test results

| Requests | Concurrency | Requests/sec | Error rate | p50 latency | p95 latency |
|---:|---:|---:|---:|---:|---:|
| 500 | 50 | *run `load-test/run.sh` and record results here* |

## What I'd add next

- Persistent job history / search (currently Redis-only, no long-term archive)
- Auth on the worker-management endpoints (currently open, fine for a local demo only)
- Horizontal scaling of the API itself behind a load balancer
- Prometheus metrics export alongside the existing WebSocket dashboard
