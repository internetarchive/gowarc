# gowarc Architecture Documentation

This directory contains comprehensive architectural documentation for the gowarc library, a sophisticated web archiving system that captures HTTP/HTTPS traffic and stores it in WARC format.

## Documentation Overview

### 📋 [Architecture Overview](architecture-overview.md)
**Start here** for a high-level understanding of the system.

**Contents:**
- High-level component architecture diagram
- Core data structures and their relationships
- Component responsibilities
- Threading model and concurrency patterns
- Configuration flow
- Metrics collection points
- Logging events catalog

**Best for:**
- New contributors understanding the system
- Architects evaluating the design
- Developers planning new features

---

### 🔄 [Request Flow](request-flow.md)
**Deep dive** into a complete request lifecycle.

**Example traced:** HTTPS request via IPv6 residential proxy → WARC file

**Contents:**
- Complete 67-step sequence diagram
- Detailed breakdown of each phase:
  - Request initiation
  - Network type selection
  - Proxy selection algorithm
  - DNS resolution
  - Connection establishment (TLS handshake)
  - Connection wrapping for capture
  - Request/response parsing
  - Deduplication logic
  - Batch assembly
  - WARC file writing
- Final WARC file structure
- Performance characteristics

**Best for:**
- Understanding data flow through the system
- Debugging request issues
- Learning how proxies and DNS work
- Understanding WARC format generation

---

### 🔧 [Component Interactions](component-interactions.md)
**Subsystem-level** architecture and interfaces.

**Contents:**
- 7 major subsystems:
  1. Client Subsystem
  2. Network Subsystem
  3. Capture Subsystem
  4. Deduplication Subsystem
  5. WARC Writing Subsystem
  6. Observability Subsystem
  7. Storage Subsystem
- Detailed interaction diagrams for each
- Inter-subsystem communication patterns
- Synchronization primitives used
- Context propagation
- Advanced features:
  - Synchronous WARC writes
  - Connection inspection
  - Random local IP
  - Custom TLS fingerprinting
- Error handling patterns
- Performance optimizations

**Best for:**
- Understanding specific subsystems
- Modifying existing components
- Adding new features
- Performance tuning

---

## Quick Reference

### Key Components by File

| Component | File | Description |
|-----------|------|-------------|
| CustomHTTPClient | `client.go` | Main entry point, extends http.Client |
| customDialer | `dialer.go` | Connection establishment, proxy selection, DNS |
| customTransport | `dialer.go` | HTTP transport with compression handling |
| CustomConnection | `dialer.go` | Wrapped connection for byte capture |
| RecordBatch | `write.go` | Groups related WARC records |
| Writer | `write.go` | WARC file writer with compression |
| recordWriter | `warc.go` | Goroutine that writes to files |
| DNS resolver | `dns.go` | Concurrent DNS lookup with caching |
| SpooledTempFile | `utils.go` | RAM/disk hybrid temp storage |
| StatsRegistry | `stats.go` | Metrics interface |
| LogBackend | `logging.go` | Logging interface |

### Key Data Flows

```
1. HTTP Request Flow:
   App → Client → Transport → Dialer → DNS/Proxy → Connection → Server

2. Response Capture Flow:
   Server → Connection → TeeReader → App
                       └→ Pipe → Parser → Batch → Writer → Disk

3. Deduplication Flow:
   Response → Digest → Local Cache ──(miss)→ Doppelganger API ──(miss)→ CDX API
                                 └(hit)→ Revisit Record
```

### Important Patterns

1. **Goroutine Coordination**
   - WaitGroup for active requests
   - Buffered channels for async communication
   - Feedback channels for sync operations

2. **Resource Management**
   - SpooledTempFile for memory efficiency
   - Connection pooling disabled (clean boundaries)
   - File rotation based on size

3. **Observability**
   - Structured logging at all layers
   - Metrics for data written, dedupe stats, proxy usage
   - Optional custom stats/log backends

4. **Fault Tolerance**
   - Proxy fallback to direct connection
   - Graceful error handling (log and continue)
   - DNS failure handling

## Diagram Notation

All diagrams use Mermaid syntax and follow these conventions:

**Colors:**
- 🔵 Blue (`#e3f2fd`) - Client/Transport layer
- 🟡 Yellow (`#fff9c4`) - Stats/Logging/Observability
- 🟠 Orange (`#fff4e1`) - Network/Dialer layer
- 🔴 Red (`#ffe1e1`) - Connection/Capture layer
- 🟢 Green (`#e1ffe1`) - WARC Writing/Batch layer
- 🟣 Purple (`#f0e1ff`) - Writer/Compression layer

**Arrows:**
- `→` Solid: Synchronous call/data flow
- `-.→` Dashed: Async/optional interaction
- `-->>` Return: Function return or response

**Shapes:**
- Rectangle: Component/process
- Cylinder: Data storage
- Diamond: Decision point
- Circle: Start/end state

## How to Use This Documentation

### For New Contributors:
1. Read [Architecture Overview](architecture-overview.md) first
2. Follow an example in [Request Flow](request-flow.md)
3. Deep dive into relevant subsystems in [Component Interactions](component-interactions.md)

### For Bug Fixing:
1. Identify the subsystem in [Component Interactions](component-interactions.md)
2. Trace the flow in [Request Flow](request-flow.md)
3. Check metrics/logging in [Architecture Overview](architecture-overview.md)

### For Feature Development:
1. Understand affected subsystems in [Component Interactions](component-interactions.md)
2. Review interfaces in [Architecture Overview](architecture-overview.md)
3. Plan data flow using [Request Flow](request-flow.md) as reference

### For Performance Tuning:
1. Check "Performance Characteristics" in [Request Flow](request-flow.md)
2. Review "Performance Optimizations" in [Component Interactions](component-interactions.md)
3. Identify metrics in [Architecture Overview](architecture-overview.md)

## Viewing Mermaid Diagrams

The documentation uses Mermaid for diagrams. To view them:

**GitHub:** Renders natively in `.md` files

**VS Code:** Install "Markdown Preview Mermaid Support" extension

**Command Line:**
```bash
# Install mermaid CLI
npm install -g @mermaid-js/mermaid-cli

# Convert to PNG
mmdc -i doc/architecture-overview.md -o doc/architecture-overview.png
```

**Online:** Copy diagram code to https://mermaid.live/

## Contributing to Documentation

When modifying the library:

1. **Update diagrams** if component interactions change
2. **Add new subsystems** to component-interactions.md
3. **Document new metrics/logs** in architecture-overview.md
4. **Trace new features** in request-flow.md if they affect the main path

Keep diagrams:
- ✅ Up-to-date with code
- ✅ Consistent in style/notation
- ✅ Focused on one concept per diagram
- ✅ Annotated with file/line references

## Additional Resources

- **Main README:** `../README.md` - Usage examples and API documentation
- **Test files:** `../*_test.go` - Concrete usage examples
- **Godoc:** Run `godoc -http=:6060` and visit http://localhost:6060/pkg/

## Questions or Issues?

If you find the documentation unclear or incorrect:
1. Open an issue describing the confusion
2. Suggest improvements or corrections
3. Submit a PR with documentation fixes

Good documentation is essential for a complex system like gowarc. Your feedback helps improve it!
