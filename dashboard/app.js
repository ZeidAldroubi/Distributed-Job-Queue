const queueDepth = document.querySelector("#queueDepth");
const processingCount = document.querySelector("#processingCount");
const jobsPerSec = document.querySelector("#jobsPerSec");
const deadCount = document.querySelector("#deadCount");
const socketState = document.querySelector("#socketState");
const feed = document.querySelector("#eventFeed");
const submitJobs = document.querySelector("#submitJobs");
const addWorkerForm = document.querySelector("#addWorkerForm");
const workerName = document.querySelector("#workerName");
const workerError = document.querySelector("#workerError");
const workerList = document.querySelector("#workers");

const workers = new Map();
const workerNamePattern = /^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$/;

function connect() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${proto}//${location.host}/ws`);

  ws.addEventListener("open", () => {
    socketState.textContent = "connected";
  });

  ws.addEventListener("close", () => {
    socketState.textContent = "reconnecting";
    setTimeout(connect, 1000);
  });

  ws.addEventListener("message", (message) => {
    const event = JSON.parse(message.data);
    renderEvent(event);
    updateWorkerFromEvent(event);
    if (event.type === "job_recovered") {
      loadWorkers();
    }
  });
}

function renderEvent(event) {
  const item = document.createElement("li");
  const at = event.time ? new Date(event.time) : new Date();
  item.innerHTML = `
    <time>${at.toLocaleTimeString()}</time>
    <b>${event.type || "event"}</b>
    <p>${formatEvent(event)}</p>
  `;
  feed.prepend(item);
  while (feed.children.length > 250) {
    feed.lastElementChild.remove();
  }
}

function formatEvent(event) {
  const parts = [];
  if (event.worker_id) parts.push(event.worker_id);
  if (event.job_id) parts.push(event.job_id);
  if (event.status) parts.push(event.status);
  if (event.message) parts.push(event.message);
  return parts.join(" | ");
}

function updateWorkerFromEvent(event) {
  if (!event.worker_id) return;
  if (event.type === "worker_deleted") {
    workers.delete(event.worker_id);
    renderWorkers();
    return;
  }

  const existing = workers.get(event.worker_id) || {
    name: event.worker_id,
    status: "running",
    current_job: "",
  };

  if (event.type === "worker_added" || event.type === "worker_relaunched") {
    existing.status = "running";
    existing.current_job = "";
  } else if (event.type === "worker_killed") {
    existing.status = "killed";
  } else if (event.type === "worker_status") {
    if (event.status === "processing") {
      existing.current_job = event.job_id || "";
    } else if (event.status === "idle") {
      existing.current_job = "";
    }
  }

  workers.set(existing.name, existing);
  renderWorkers();
}

async function loadWorkers() {
  try {
    const response = await fetch("/workers");
    if (!response.ok) throw new Error(await response.text());
    const data = await response.json();
    workers.clear();
    for (const worker of data) {
      workers.set(worker.name, worker);
    }
    renderWorkers();
  } catch (error) {
    workerError.textContent = error.message || "workers unavailable";
  }
}

function renderWorkers() {
  workerList.replaceChildren();
  const sorted = [...workers.values()].sort((a, b) => a.name.localeCompare(b.name));
  if (sorted.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty-workers";
    empty.textContent = "No workers running";
    workerList.append(empty);
    return;
  }

  for (const worker of sorted) {
    const card = document.createElement("article");
    card.className = `worker ${worker.status === "killed" ? "offline" : ""} ${worker.current_job ? "processing" : ""}`;
    card.innerHTML = `
      <div>
        <span>${worker.name}</span>
        <strong>${worker.status}</strong>
        <p>${worker.current_job ? `job ${worker.current_job}` : "idle"}</p>
      </div>
      <div class="worker-actions"></div>
    `;

    const actions = card.querySelector(".worker-actions");
    if (worker.status === "running") {
      actions.append(workerButton("Kill", "danger", () => postWorkerAction(worker.name, "kill")));
    }
    if (worker.status === "killed") {
      actions.append(workerButton("Relaunch", "", () => postWorkerAction(worker.name, "relaunch")));
    }
    actions.append(workerButton("Delete", "danger secondary", () => deleteWorker(worker.name)));
    workerList.append(card);
  }
}

function workerButton(label, className, onClick) {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = label;
  if (className) button.className = className;
  button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      await onClick();
    } finally {
      button.disabled = false;
    }
  });
  return button;
}

async function addWorker(event) {
  event.preventDefault();
  workerError.textContent = "";
  const name = workerName.value.trim();
  if (!workerNamePattern.test(name)) {
    workerError.textContent = "Use only letters, numbers, and hyphens.";
    return;
  }
  const response = await fetch("/workers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!response.ok) {
    workerError.textContent = (await response.text()).trim();
    return;
  }
  workerName.value = "";
}

async function postWorkerAction(name, action) {
  const response = await fetch(`/workers/${encodeURIComponent(name)}/${action}`, { method: "POST" });
  if (!response.ok) {
    renderEvent({ type: "action_failed", message: await response.text(), time: new Date().toISOString() });
  }
}

async function deleteWorker(name) {
  if (!confirm(`Permanently delete ${name}?`)) return;
  const response = await fetch(`/workers/${encodeURIComponent(name)}`, { method: "DELETE" });
  if (!response.ok) {
    renderEvent({ type: "action_failed", message: await response.text(), time: new Date().toISOString() });
  }
}

async function pollStats() {
  try {
    const response = await fetch("/stats");
    const stats = await response.json();
    queueDepth.textContent = stats.queue_depth ?? 0;
    processingCount.textContent = stats.processing ?? 0;
    deadCount.textContent = stats.dead ?? 0;
    jobsPerSec.textContent = Number(stats.jobs_per_sec ?? 0).toFixed(1);
  } catch {
    socketState.textContent = "stats unavailable";
  }
}

async function postAction(button, url) {
  button.disabled = true;
  try {
    const response = await fetch(url, { method: "POST" });
    if (!response.ok) {
      renderEvent({ type: "action_failed", message: await response.text(), time: new Date().toISOString() });
    }
  } finally {
    button.disabled = false;
  }
}

submitJobs.addEventListener("click", () => postAction(submitJobs, "/jobs/bulk-test"));
addWorkerForm.addEventListener("submit", addWorker);

connect();
loadWorkers();
pollStats();
setInterval(pollStats, 1000);
