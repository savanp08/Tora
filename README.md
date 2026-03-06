# Tora

<p align="center">
  <img src="docs/logo.png" width="120" alt="Tora logo">
</p>

<p align="center">
  <b>Real-time sessions for chat, code, whiteboarding, and calls.</b>
</p>

<p align="center">
  A privacy-first collaboration workspace that keeps conversation, code, and context in one session.
</p>

<p align="center">
  <img src="docs/media/hero-demo.gif" width="900" alt="Tora hero demo">
</p>

<p align="center">
  <sub>Replace <code>docs/media/hero-demo.gif</code> with a 10-20 second product demo.</sub>
</p>

<p align="center">
  <a href="https://kit.svelte.dev/"><img src="https://img.shields.io/badge/SvelteKit-5.x-ff3e00?logo=svelte&logoColor=white" alt="SvelteKit"></a>
  <a href="https://www.typescriptlang.org/"><img src="https://img.shields.io/badge/TypeScript-5.x-3178c6?logo=typescript&logoColor=white" alt="TypeScript"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="https://redis.io/"><img src="https://img.shields.io/badge/Redis-7.x-dc382d?logo=redis&logoColor=white" alt="Redis"></a>
  <a href="https://www.scylladb.com/"><img src="https://img.shields.io/badge/ScyllaDB-supported-6cd4ff" alt="ScyllaDB"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>

<p align="center">
  <a href="#getting-started">Quick Start</a> •
  <a href="#features">Features</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#deployment">Deployment</a>
</p>

---

## Product Overview

Tora is a session-based collaboration platform for fast, high-context sessions.

Teams create or join a session and collaborate across messaging, whiteboarding, coding, calls, and file exchange in one workspace.

The goal is simple: reduce tool switching and keep discussions, artifacts, and execution context aligned.

---

## Why This Exists

You can open a workspace without login friction, save information online, and return later with context intact.

Modern collaboration is often split across separate tools for messaging, whiteboarding, coding, and calls.

That split introduces context loss and slows decision-making.

Tora uses a temporary, session-centric model so teams can collaborate end-to-end without switching tools.

---

## Comparison

| Feature | Tora | Slack | Discord |
|---|---|---|---|
| Ephemeral sessions | ✅ | ❌ | ❌ |
| Session branching | ✅ | ❌ | ❌ |
| Collaborative code canvas | ✅ | ❌ | ❌ |
| Built-in drawboard workspace | ✅ | ❌ | ❌ |
| No account required (guest-first flow) | ✅ | ❌ | ❌ |
| In-session voice and video calls | ✅ | ✅ | ✅ |
| File and media sharing | ✅ | ✅ | ✅ |

Comparison reflects built-in capabilities for session-centric collaboration.

---

## Collaboration Tools

Each session includes:

- **Real-Time Chat:** messaging, presence, replies, and pinning.
- **Drawboard Workspace:** shared board for diagrams, notes, and visual planning.
- **Code Canvas:** collaborative editing with shared execution output.
- **Voice and Video Calls:** built-in WebRTC calls inside each session.
- **File and Media Sharing:** session-scoped uploads for files, media, and voice notes.
- **Branchable Sessions:** split sessions into focused child sessions with shared context.

---

## Features

Detailed feature breakdown.

<sub>Media placeholders are embedded with each feature below. Replace files in <code>docs/media/</code> as needed.</sub>

### Real-Time Chat

Low-latency session messaging designed for active collaboration.

- Instant session messaging
- Typing indicators and online presence
- Replies, pinning, and context-aware navigation
- Session message history loading

<p align="center">
  <img src="docs/media/realtime-chat.gif" width="900" alt="Real-Time Chat">
</p>

### Drawboard Workspace

Shared visual board for ideation and planning.

- Infinite pan and zoom canvas
- Free draw, shapes, text, and sticky notes
- Cursor presence for collaborators
- Embedded message and media cards on the board

<p align="center">
  <img src="docs/media/drawboard.gif" width="900" alt="Drawboard Workspace">
</p>

### Code Canvas

Shared coding workspace with live collaborative editing.

- Monaco editor with a project-style file tree
- CRDT collaboration with Yjs
- Shared execution output stream
- Canvas snapshot save and load
- Snippet-to-chat handoff

Supported runtimes:

- Python (Pyodide worker)
- JavaScript (worker runtime)

<p align="center">
  <img src="docs/media/code-canvas.gif" width="900" alt="Code Canvas">
</p>

### Voice and Video Calls

WebRTC calls built directly into each session.

- Audio and video calls
- Session header call invites
- Minimized call state with restore
- Call activity in the timeline

<p align="center">
  <img src="docs/media/video-call.gif" width="900" alt="Voice and Video Calls">
</p>

### File and Media Sharing

Session-scoped upload and attachment workflow.

- File and media attachments
- Voice note recording
- Presigned upload support
- Object storage-backed retrieval

<p align="center">
  <img src="docs/media/uploads.gif" width="900" alt="File and Media Sharing">
</p>

### Branchable Sessions

Split active conversations into focused child sessions while preserving context.

- Parent and child session context
- Temporary sub-session workflows
- Parallel collaboration tracks per topic

<p align="center">
  <img src="docs/media/session-branch.gif" width="900" alt="Branchable Sessions">
</p>

---

## Use Cases

### Engineering Discussions

Spin up a temporary session to debug incidents with chat, code, and calls in one workflow.

### Architecture Brainstorming

Sketch system flows on the drawboard while discussing tradeoffs in real time.

### Pair Programming

Write and run code together in the shared canvas.

### Hackathons

Create session-based collaboration spaces for teams working in parallel.

### Study Groups

Discuss problems, sketch diagrams, and exchange runnable snippets during sessions.

---

## Architecture

```text
Browser
   │
   ▼
SvelteKit Frontend
   │
   ▼
Go API Server
   │
 ┌─┴─────────────┐
 ▼               ▼
Redis        ScyllaDB / Astra
(real-time)   (storage)
   │
   ▼
Cloudflare R2
(file storage)
```

```text
SvelteKit Frontend
├ Chat UI / Drawboard / Code Canvas
├ Monaco + Yjs collaboration
├ Media upload components
└ WebSocket client

Go Backend
├ Auth + session lifecycle APIs
├ Message + pin + upload APIs
├ Canvas snapshot + board APIs
└ WebSocket hub (presence, typing, events)

Execution
├ Python runtime worker (Pyodide)
└ JavaScript runtime worker
```

---

## Security & Privacy

Tora includes practical controls for secure collaboration.

- Minimal identity requirements
- Optional password-protected session access
- Scoped session access
- WebSocket authentication
- Configurable usage and quota limits
- Isolated object storage for uploads
- HTTPS and WSS support in production

Secrets and environment-specific credentials are managed through environment variables and should never be committed.

---

## Tech Stack

### Frontend

- **SvelteKit**
- **TypeScript**
- Monaco Editor
- Yjs CRDT collaboration
- xterm.js
- Web Workers

### Backend

- **Go**
- Chi router
- Gorilla WebSocket
- Redis
- ScyllaDB / Astra DB

### Infrastructure

- Docker Compose
- Cloudflare R2
- Optional Piston runtime
- Optional Caddy reverse proxy

---

## Getting Started

### Prerequisites

- Node.js 20+
- Go 1.24+
- Docker and Docker Compose

### Install Dependencies

```bash
npm install
cd backend
go mod download
cd ..
```

### Start Infrastructure

```bash
docker compose up -d redis piston
```

### Start Backend

```bash
cd backend
go run ./cmd/server/main.go
```

Backend runs on:

```text
http://localhost:8080
```

### Start Frontend

```bash
npm run dev
```

Frontend runs on:

```text
http://localhost:5173
```

---

## Deployment

Tora can run locally or in production with separate frontend, API, state, and storage layers.

Recommended production setup:

```text
Frontend
↓
Go API Server
↓
Redis + ScyllaDB
↓
Object Storage (Cloudflare R2)
```

Docker Compose is included for local infrastructure orchestration and development.

---

## Contributing

Contributions are welcome.

1. Fork the repository.
2. Create a feature branch.
3. Commit your changes.
4. Submit a pull request.

Please ensure code passes checks before submitting:

```bash
# Frontend
npm run check
npm run lint
npm run test

# Backend
cd backend
GOCACHE=/tmp/go-build-cache go test ./...
```

---

## Community

If you have questions, ideas, or issues:

- Open an issue
- Start a discussion
- Submit a feature request

---

<!-- ## Roadmap

Potential improvements include:

- OpenAPI documentation
- End-to-end test suites
- Observability dashboards
- Distributed WebSocket scaling
- Plugin architecture
- AI collaboration assistants

---

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=savanp08/tora&type=Date)](https://star-history.com/#savanp08/tora&Date)

--- -->

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
