# Architecture Diagram Source

```mermaid
flowchart LR
  Client[Client / Dashboard] --> API[Go API :8080]
  API --> Redis[(Redis lists + job JSON)]
  Redis --> W1[Worker 1]
  Redis --> W2[Worker 2]
  Redis --> W3[Worker 3]
  W1 --> Output[/output]
  W2 --> Output
  W3 --> Output
  W1 --> Events[Redis Pub/Sub events]
  W2 --> Events
  W3 --> Events
  Events --> API
  API --> WS[WebSocket /ws]
  WS --> Client
```
