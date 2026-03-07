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

## What Tora Is

Tora is a real-time collaboration workspace built for **focused sessions** where conversation, code, and visual thinking happen together.

Instead of switching between chat apps, whiteboards, code editors, and calls, participants join a shared session that keeps everything in one place.

A session can act as a debugging room, architecture board, pair-programming space, or quick meeting environment.

---

## Core Characteristics

Tora is designed around a few simple principles:

- **Ephemeral sessions**

  Sessions are designed for temporary collaboration rather than long-term message storage.

- **Real-time interaction**

  Messages, drawings, code edits, and presence updates propagate instantly between participants.

- **Shared context**

  Conversations, diagrams, files, and code all live inside the same environment.

- **Session-centric collaboration**

  Participants collaborate inside a shared space rather than through persistent user identities.

- **Self-hostable**

  Teams can run their own instance and maintain full control over infrastructure.

---

## Features

### Messaging

- Real-time chat with presence indicators
- Typing indicators and reply navigation
- Message pinning for important discussions

### Collaborative Coding

- Monaco-based shared code canvas
- Multiple participants editing simultaneously
- Code snippets shareable inside chat
- Execution output inside the workspace

### Visual Collaboration

- Shared drawboard for sketches and diagrams
- Shapes, annotations, and quick notes
- Live cursor presence

### Calls

- Integrated WebRTC audio and video sessions
- Join calls directly from the workspace
- Continue collaborating while calls run

### Media and File Exchange

- Upload and share files within a session
- Image previews and attachments
- Voice message support

### Branching Sessions

- Create new sessions derived from an existing discussion
- Explore ideas without interrupting the main conversation
- Maintain contextual relationships between sessions

---

## Why Tora Exists

Modern collaboration is fragmented.

A typical workflow requires multiple tools:

- messaging platforms for conversation
- whiteboards for diagrams
- code editors for debugging
- video calls for discussion

Switching between these tools breaks context and slows collaboration.

Tora brings these workflows together inside a single temporary workspace so that teams can communicate, experiment, and brainstorm without losing momentum.

---

## Example Use Cases

Tora can support many collaborative scenarios:

- **Engineering debugging sessions**

  Investigate production issues while sharing code and discussing fixes.

- **Architecture discussions**

  Sketch diagrams and annotate ideas while talking in real time.

- **Pair programming**

  Collaboratively write and run code inside the shared canvas.

- **Hackathons**

  Quickly create workspaces for teams during rapid development events.

- **Study groups**

  Discuss problems, draw diagrams, and share runnable snippets.

---

## A Note From The Developer

Tora started as an experiment in building a collaboration environment where conversation and problem-solving tools coexist.

Most platforms specialize in one type of interaction: messaging, meetings, or documentation.

Tora explores the idea that a **temporary workspace can host the entire collaboration process**.

---

## Architecture

Tora uses a real-time collaboration stack designed for responsiveness.

```text
Browser
│
▼
SvelteKit Frontend
│
▼
Go Backend API
│
┌─┴─────────────┐
▼               ▼
Redis        ScyllaDB
(real-time)   (persistent metadata)
│
▼
Object Storage
(media and snapshots)
```

### Frontend

- SvelteKit
- TypeScript
- Monaco Editor
- Yjs CRDT collaboration
- Web Workers

### Backend

- Go API server
- WebSocket hub for real-time communication
- Redis pub/sub for ephemeral state
- ScyllaDB for durable data

---

## Getting Started

### Prerequisites

- Node.js 20+
- Go 1.24+
- Docker + Docker Compose

---

### Install dependencies

```bash
npm install
cd backend
go mod download
cd ..
```

---

### Start infrastructure

```bash
docker compose up -d redis piston
```

---

### Start backend

```bash
cd backend
go run ./cmd/server/main.go
```

Backend runs at

```text
http://localhost:8080
```

---

### Start frontend

```bash
npm run dev
```

Frontend runs at

```text
http://localhost:5173
```

---

## Deployment

Typical production deployment:

```text
Frontend
   │
   ▼
Go API Server
   │
   ▼
Redis + ScyllaDB
   │
   ▼
Object Storage (S3 / R2)
```

Infrastructure services can be orchestrated using Docker Compose or container orchestration platforms.

---

## Contributing

Contributions are welcome.

1. Fork the repository
2. Create a feature branch
3. Implement changes
4. Submit a pull request

Before submitting:

```bash
npm run check
npm run lint
npm run test
```

Backend tests:

```bash
cd backend
go test ./...
```

---

## License

This project is licensed under the MIT License.
