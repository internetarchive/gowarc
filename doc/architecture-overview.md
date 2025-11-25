# Go WARC Library - Architecture Overview

This document provides a comprehensive overview of the gowarc library architecture and component interactions.

## High-Level Component Architecture

```mermaid
graph TB
    subgraph "User Application"
        APP[Application Code]
    end

    subgraph "HTTP Client Layer"
        CLIENT[CustomHTTPClient<br/>- http.Client wrapper<br/>- Dedup hash table<br/>- WARC writer channel<br/>- Stats/Logging]
        TRANSPORT[customTransport<br/>- Force gzip header<br/>- Decompress responses]
        DIALER[customDialer<br/>- Proxy selection<br/>- DNS resolution<br/>- Connection wrapping]
    end

    subgraph "Network Layer"
        DNS[DNS Resolver<br/>- Concurrent lookup<br/>- TTL cache<br/>- WARC recording]
        PROXY[Proxy Selection<br/>- Type filtering<br/>- Network filtering<br/>- Domain filtering<br/>- Round-robin LB]
        CONN[CustomConnection<br/>- Bidirectional capture<br/>- TeeReader/MultiWriter<br/>- Read deadline]
    end

    subgraph "Capture Layer"
        PIPES[IO Pipes<br/>- Request pipe<br/>- Response pipe]
        PARSE[HTTP Parser<br/>- Request/Response<br/>- Extract metadata<br/>- Calculate digests]
        DEDUPE[Deduplication<br/>- Local hash table<br/>- Doppelganger API<br/>- CDX API]
    end

    subgraph "WARC Writing Pipeline"
        BATCH[RecordBatch Channel<br/>- Buffered channel<br/>- Multiple writers]
        WRITER[recordWriter Goroutines<br/>- Pool of writers<br/>- File rotation<br/>- Compression]
        DISK[WARC Files<br/>- .warc.gz/.warc.zst<br/>- Auto-rotation<br/>- Sequential naming]
    end

    subgraph "Supporting Systems"
        STATS[Stats Registry<br/>- Counters<br/>- Gauges<br/>- Histograms]
        LOG[Log Backend<br/>- slog interface<br/>- Debug/Info/Warn/Error]
        TEMP[Temp File System<br/>- SpooledTempFile<br/>- RAM → Disk threshold]
    end

    APP --> CLIENT
    CLIENT --> TRANSPORT
    TRANSPORT --> DIALER

    DIALER --> DNS
    DIALER --> PROXY
    DIALER --> CONN

    CONN --> PIPES
    PIPES --> PARSE
    PARSE --> DEDUPE

    DEDUPE --> BATCH
    BATCH --> WRITER
    WRITER --> DISK

    CLIENT -.-> STATS
    CLIENT -.-> LOG
    DIALER -.-> STATS
    DIALER -.-> LOG
    PARSE -.-> TEMP
    WRITER -.-> STATS
    WRITER -.-> LOG

    style CLIENT fill:#e1f5ff
    style DIALER fill:#fff4e1
    style CONN fill:#ffe1e1
    style BATCH fill:#e1ffe1
    style WRITER fill:#f0e1ff
```

## Core Data Structures

```mermaid
classDiagram
    class CustomHTTPClient {
        +http.Client
        +WARCWriter chan RecordBatch
        +dedupeHashTable sync.Map
        +WaitGroup
        +statsRegistry StatsRegistry
        +logBackend LogBackend
        +TempDir string
        +DigestAlgorithm
        +Do(req) Response
        +Close() error
    }

    class customDialer {
        +proxyDialers []proxyDialerInfo
        +proxyRoundRobinIndex atomic.Uint32
        +DNSRecords Cache
        +allowDirectFallback bool
        +disableIPv4 bool
        +disableIPv6 bool
        +CustomDialContext(ctx, network, addr) Conn
        +CustomDialTLSContext(ctx, network, addr) Conn
    }

    class CustomConnection {
        +net.Conn
        +io.Reader (TeeReader)
        +io.Writer (MultiWriter)
        +closers []PipeWriter
        +connReadDeadline Duration
        +Read(b) int
        +Write(b) int
        +Close() error
    }

    class RecordBatch {
        +Records []Record
        +CaptureTime string
        +FeedbackChan chan
    }

    class Record {
        +Header map~string~string
        +Content ReadWriteSeekCloser
        +Version string
        +Offset int64
        +Size int64
    }

    class Writer {
        +FileWriter bufio.Writer
        +GZIPWriter GzipWriter
        +ZSTDWriter Encoder
        +FileName string
        +Compression string
        +WriteRecord(record) error
    }

    class ProxyConfig {
        +URL string
        +Network ProxyNetwork
        +Type ProxyType
        +AllowedDomains []string
    }

    CustomHTTPClient --> customDialer : uses
    CustomHTTPClient --> RecordBatch : sends
    customDialer --> CustomConnection : creates
    customDialer --> ProxyConfig : configures
    CustomConnection --> RecordBatch : captured as
    RecordBatch --> Record : contains
    Writer --> Record : writes
```

## Component Responsibilities

### CustomHTTPClient
- **Purpose**: Main entry point for making HTTP requests with WARC recording
- **Key Features**:
  - Extends standard `http.Client`
  - Manages deduplication hash table
  - Coordinates WARC writer goroutines
  - Tracks active requests with WaitGroup
  - Integrates stats and logging

### customDialer
- **Purpose**: Establishes network connections with proxy support and DNS resolution
- **Key Features**:
  - Proxy selection and filtering (type, network, domain)
  - Concurrent DNS resolution with caching
  - Connection wrapping for byte capture
  - TLS handshake with custom fingerprinting
  - Load balancing across proxies

### CustomConnection
- **Purpose**: Transparent connection wrapper that captures all traffic
- **Key Features**:
  - Bidirectional byte capture using io.Pipe
  - TeeReader for response interception
  - MultiWriter for request interception
  - Configurable read deadlines
  - Automatic goroutine launch for WARC creation

### RecordBatch & Record
- **Purpose**: Represent HTTP request/response pairs for archival
- **Key Features**:
  - Groups related records (request + response)
  - Shared capture timestamp
  - Optional feedback channel for synchronous writes
  - SpooledTempFile content for memory efficiency

### Writer & RotatorSettings
- **Purpose**: Write WARC records to compressed files with rotation
- **Key Features**:
  - Multi-threaded writer pool
  - Automatic file rotation based on size
  - GZIP/ZSTD compression support
  - Dictionary compression for ZSTD
  - Sequential file naming with collision avoidance

## Threading Model

```mermaid
sequenceDiagram
    participant App as Application
    participant Client as CustomHTTPClient
    participant Dialer as customDialer
    participant Conn as CustomConnection
    participant WG as writeWARCFromConnection
    participant Writers as recordWriter Pool
    participant Disk as WARC Files

    App->>Client: Do(request)
    Client->>Dialer: DialContext/DialTLSContext
    Dialer->>Conn: Create wrapped connection

    activate Conn
    Conn->>WG: Launch goroutine
    activate WG
    Conn-->>Dialer: Return connection

    Dialer-->>Client: Return connection
    Client->>App: Return response

    Note over App,Conn: App reads response body

    WG->>WG: Parse request in goroutine
    WG->>WG: Parse response in goroutine
    WG->>WG: Calculate digests
    WG->>WG: Check deduplication
    WG->>Client: Send RecordBatch to channel
    deactivate WG
    deactivate Conn

    Client->>Writers: RecordBatch via channel

    activate Writers
    Writers->>Writers: Check file size, rotate if needed
    Writers->>Disk: Write compressed records
    Writers-->>Client: Signal via FeedbackChan (optional)
    deactivate Writers
```

## Configuration Flow

```mermaid
graph LR
    subgraph "HTTPClientSettings"
        RS[RotatorSettings]
        PROXIES[Proxies Config]
        DNS_CFG[DNS Config]
        DEDUPE[Dedupe Options]
        TIMEOUTS[Timeouts]
        FLAGS[Feature Flags]
        STATS_CFG[Stats Registry]
        LOG_CFG[Log Backend]
    end

    subgraph "Initialization"
        INIT[NewWARCWritingHTTPClient]
    end

    subgraph "Created Components"
        CLIENT[CustomHTTPClient]
        DIALER_INST[customDialer]
        TRANSPORT_INST[customTransport]
        ROTATOR[recordWriter Pool]
    end

    RS --> INIT
    PROXIES --> INIT
    DNS_CFG --> INIT
    DEDUPE --> INIT
    TIMEOUTS --> INIT
    FLAGS --> INIT
    STATS_CFG --> INIT
    LOG_CFG --> INIT

    INIT --> CLIENT
    INIT --> DIALER_INST
    INIT --> TRANSPORT_INST
    INIT --> ROTATOR

    CLIENT --> DIALER_INST
    CLIENT --> TRANSPORT_INST
    CLIENT --> ROTATOR

    style INIT fill:#ffdddd
    style CLIENT fill:#ddffdd
```

## Metrics Collection Points

```mermaid
graph TB
    subgraph "Request Metrics"
        PROXY_REQ[proxy_requests_total<br/>Label: proxy name]
        PROXY_ERR[proxy_errors_total<br/>Label: proxy name]
        PROXY_LAST[proxy_last_used_nanoseconds<br/>Label: proxy name]
    end

    subgraph "Deduplication Metrics"
        LOCAL_COUNT[local_deduped_total]
        LOCAL_BYTES[local_deduped_bytes_total]
        DOPPEL_COUNT[doppelganger_deduped_total]
        DOPPEL_BYTES[doppelganger_deduped_bytes_total]
        CDX_COUNT[cdx_deduped_total]
        CDX_BYTES[cdx_deduped_bytes_total]
    end

    subgraph "Writing Metrics"
        TOTAL_WRITTEN[total_data_written]
    end

    subgraph "StatsRegistry"
        REGISTRY[User-provided or<br/>Local Registry]
    end

    PROXY_REQ --> REGISTRY
    PROXY_ERR --> REGISTRY
    PROXY_LAST --> REGISTRY
    LOCAL_COUNT --> REGISTRY
    LOCAL_BYTES --> REGISTRY
    DOPPEL_COUNT --> REGISTRY
    DOPPEL_BYTES --> REGISTRY
    CDX_COUNT --> REGISTRY
    CDX_BYTES --> REGISTRY
    TOTAL_WRITTEN --> REGISTRY

    style REGISTRY fill:#ffffdd
```

## Logging Events

The library emits structured logs at various levels:

### Debug Level
- Proxy selection decisions
- DNS resolution results
- Connection establishment details
- TLS handshake success

### Info Level
- WARC file creation
- WARC file rotation
- WARC writer shutdown

### Error Level
- DNS resolution failures
- Proxy connection failures
- Direct connection failures
- TLS handshake failures
- WARC record writing errors
- Digest calculation errors
- HTTP parsing errors
- DiscardHook rejections

All log events include contextual key-value pairs for filtering and debugging.
