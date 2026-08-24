module distributed-job-queue/worker

go 1.22

require (
	distributed-job-queue/shared v0.0.0
	github.com/disintegration/imaging v1.6.2
	github.com/go-redis/redis/v9 v9.0.0-rc.2
	github.com/google/uuid v1.6.0
)

require (
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	golang.org/x/image v0.0.0-20191009234506-e7c1f5e7dbb8 // indirect
)

replace distributed-job-queue/shared => ../shared
