# Component Interactions and Subsystems

This document breaks down the gowarc library into logical subsystems and shows how they interact with each other.

## Subsystem Architecture

```mermaid
graph TB
    subgraph "Client Subsystem"
        direction TB
        CLIENT[CustomHTTPClient]
        SETTINGS[HTTPClientSettings]
        TRANSPORT[customTransport]
        SETTINGS --> CLIENT
        CLIENT --> TRANSPORT
    end

    subgraph "Network Subsystem"
        direction TB
        DIALER[customDialer]
        PROXY[Proxy Selector]
        DNS[DNS Resolver]
        CONN[CustomConnection]
        DIALER --> PROXY
        DIALER --> DNS
        DIALER --> CONN
    end

    subgraph "Capture Subsystem"
        direction TB
        PIPES[IO Pipes]
        PARSER[HTTP Parser]
        METADATA[Metadata Extractor]
        PIPES --> PARSER
        PARSER --> METADATA
    end

    subgraph "Deduplication Subsystem"
        direction TB
        LOCAL[Local Hash Table]
        DOPPEL[Doppelganger API]
        CDX[CDX API]
        DEDUPE_LOGIC[Dedupe Logic]
        DEDUPE_LOGIC --> LOCAL
        DEDUPE_LOGIC --> DOPPEL
        DEDUPE_LOGIC --> CDX
    end

    subgraph "WARC Writing Subsystem"
        direction TB
        BATCH[RecordBatch Channel]
        ROTATOR[File Rotator]
        COMPRESS[Compression]
        WRITER[File Writer]
        BATCH --> ROTATOR
        ROTATOR --> COMPRESS
        COMPRESS --> WRITER
    end

    subgraph "Observability Subsystem"
        direction TB
        STATS[Stats Registry]
        LOG[Log Backend]
    end

    subgraph "Storage Subsystem"
        direction TB
        SPOOL[SpooledTempFile]
        DISK[Disk I/O]
        SPOOL --> DISK
    end

    TRANSPORT --> DIALER
    CONN --> PIPES
    METADATA --> DEDUPE_LOGIC
    DEDUPE_LOGIC --> BATCH

    CLIENT -.-> STATS
    CLIENT -.-> LOG
    DIALER -.-> STATS
    DIALER -.-> LOG
    DEDUPE_LOGIC -.-> STATS
    ROTATOR -.-> STATS
    ROTATOR -.-> LOG

    PARSER -.-> SPOOL
    BATCH -.-> SPOOL

    style CLIENT fill:#e3f2fd
    style DIALER fill:#fff3e0
    style PIPES fill:#fce4ec
    style DEDUPE_LOGIC fill:#f3e5f5
    style BATCH fill:#e8f5e9
    style STATS fill:#fff9c4
    style SPOOL fill:#ffe0b2
```

## Subsystem Details

### 1. Client Subsystem

**Purpose:** Entry point and coordination of all other subsystems

**Components:**
- `CustomHTTPClient` - Main client that extends `http.Client`
- `HTTPClientSettings` - Configuration structure
- `customTransport` - Custom HTTP transport layer

**Key Interactions:**

```mermaid
sequenceDiagram
    participant User
    participant Client as CustomHTTPClient
    participant Settings as HTTPClientSettings
    participant Transport as customTransport

    User->>Settings: Create configuration
    User->>Client: NewWARCWritingHTTPClient(settings)
    activate Client
    Client->>Client: Initialize stats registry
    Client->>Client: Initialize log backend
    Client->>Client: Create deduplication table
    Client->>Client: Setup WARC writer pool
    Client->>Transport: Create custom transport
    Transport-->>Client: Return configured transport
    Client-->>User: Return client
    deactivate Client

    User->>Client: Do(request)
    Client->>Transport: RoundTrip(request)
    Transport-->>Client: Return response
    Client-->>User: Return response
```

**Responsibilities:**
- Initialize all subsystems
- Manage deduplication hash table
- Coordinate WARC writer goroutines
- Track active requests via WaitGroup
- Provide public API surface

### 2. Network Subsystem

**Purpose:** Establish network connections with proxy and DNS support

**Components:**
- `customDialer` - Connection establishment logic
- Proxy selection algorithm
- DNS resolution with caching
- `CustomConnection` - Wrapped connection

**Proxy Selection Logic:**

```mermaid
flowchart TD
    START[selectProxy called]

    CTX_TYPE{Context specifies<br/>ProxyType?}
    START --> CTX_TYPE

    CTX_TYPE -->|Yes| FILTER_TYPE[Filter: Keep only<br/>matching ProxyType]
    CTX_TYPE -->|No| FILTER_ANY[Filter: Keep only<br/>ProxyTypeAny]

    FILTER_TYPE --> FILTER_NET
    FILTER_ANY --> FILTER_NET

    FILTER_NET[Filter: Network<br/>IPv4/IPv6 compatibility]

    FILTER_NET --> FILTER_DOM[Filter: Domain<br/>glob patterns]

    FILTER_DOM --> CHECK_EMPTY{Any proxies<br/>remaining?}

    CHECK_EMPTY -->|Yes| ROUND_ROBIN[Round-robin select<br/>atomic counter]
    CHECK_EMPTY -->|No| CHECK_FALLBACK{Direct fallback<br/>enabled?}

    CHECK_FALLBACK -->|Yes| RETURN_NIL[Return nil<br/>Use direct connection]
    CHECK_FALLBACK -->|No| RETURN_ERR[Return error]

    ROUND_ROBIN --> UPDATE_STATS[Update metrics:<br/>proxy_requests_total<br/>proxy_last_used]

    UPDATE_STATS --> RETURN_PROXY[Return selected proxy]

    style FILTER_TYPE fill:#ffebee
    style FILTER_NET fill:#fff3e0
    style FILTER_DOM fill:#e8f5e9
    style ROUND_ROBIN fill:#e3f2fd
    style UPDATE_STATS fill:#fff9c4
```

**DNS Resolution Flow:**

```mermaid
flowchart TD
    START[archiveDNS called]

    CACHE_CHECK{In cache?}
    START --> CACHE_CHECK

    CACHE_CHECK -->|Yes| RETURN_CACHED[Return cached IP]
    CACHE_CHECK -->|No| CONCURRENT[concurrentDNSLookup]

    CONCURRENT --> WORKER_POOL[Create worker pool<br/>size = dnsConcurrency]

    WORKER_POOL --> ROUND_ROBIN[Round-robin<br/>distribute servers]

    ROUND_ROBIN --> SPAWN[Spawn workers]

    SPAWN --> QUERY_A[Query A records<br/>IPv4]
    SPAWN --> QUERY_AAAA[Query AAAA records<br/>IPv6]

    QUERY_A --> BOTH_FOUND{Both A and AAAA<br/>found?}
    QUERY_AAAA --> BOTH_FOUND

    BOTH_FOUND -->|Yes| CANCEL[Cancel remaining<br/>queries early]
    BOTH_FOUND -->|No| CONTINUE[Continue querying]

    CANCEL --> SELECT_IP
    CONTINUE --> SELECT_IP[Select IP:<br/>Prefer IPv6 if available]

    SELECT_IP --> CACHE_STORE[Store in cache<br/>with TTL]

    CACHE_STORE --> WRITE_WARC[Write DNS response<br/>to WARC]

    WRITE_WARC --> RETURN[Return IP]

    style CACHE_CHECK fill:#e8f5e9
    style WORKER_POOL fill:#e3f2fd
    style QUERY_A fill:#ffebee
    style QUERY_AAAA fill:#f3e5f5
    style CACHE_STORE fill:#fff9c4
```

**Connection Wrapping:**

```mermaid
graph LR
    subgraph "Real Network"
        SERVER[Server]
    end

    subgraph "CustomConnection"
        READER[TeeReader]
        WRITER[MultiWriter]
    end

    subgraph "Application"
        APP[App Read/Write]
    end

    subgraph "Capture Pipes"
        REQ_PIPE[Request Pipe]
        RESP_PIPE[Response Pipe]
    end

    subgraph "WARC Goroutine"
        WARC[writeWARCFromConnection]
    end

    APP -->|Write request| WRITER
    WRITER -->|Original| SERVER
    WRITER -->|Copy| REQ_PIPE
    REQ_PIPE --> WARC

    SERVER -->|Response| READER
    READER -->|Original| APP
    READER -->|Copy| RESP_PIPE
    RESP_PIPE --> WARC

    style READER fill:#e3f2fd
    style WRITER fill:#ffebee
    style WARC fill:#e8f5e9
```

### 3. Capture Subsystem

**Purpose:** Parse and extract metadata from HTTP traffic

**Components:**
- IO pipes for request/response capture
- HTTP request/response parsers
- Metadata extraction (URI, headers, timing)

**Request/Response Processing:**

```mermaid
stateDiagram-v2
    [*] --> CreatePipes: wrapConnection()
    CreatePipes --> LaunchGoroutine: spawn writeWARCFromConnection
    LaunchGoroutine --> SpawnParsers: create io.Pipes

    state SpawnParsers {
        [*] --> ReqParser: go readRequest()
        [*] --> RespParser: go readResponse()
    }

    state ReqParser {
        [*] --> CreateReqRecord
        CreateReqRecord --> CopyBytes: io.Copy to SpooledTempFile
        CopyBytes --> ParseHTTP: http.ReadRequest
        ParseHTTP --> ExtractURI: Parse request line + Host
        ExtractURI --> SendURI: channel → response parser
        SendURI --> SetHeaders: WARC headers
        SetHeaders --> SendRecord: channel → assembler
    }

    state RespParser {
        [*] --> WaitURI: Receive from request parser
        WaitURI --> CreateRespRecord
        CreateRespRecord --> CopyRespBytes: io.Copy to SpooledTempFile
        CopyRespBytes --> ParseResp: http.ReadResponse
        ParseResp --> CheckDiscard: DiscardHook?
        CheckDiscard --> CalcPayloadDigest: Hash response body
        CalcPayloadDigest --> CheckDedupe: Query dedup systems
        CheckDedupe --> CalcBlockDigest: Hash entire record
        CalcBlockDigest --> SendRespRecord: channel → assembler
    }

    SpawnParsers --> WaitBoth: sync.WaitGroup
    WaitBoth --> AssembleBatch: Create RecordBatch
    AssembleBatch --> SetMetadata: UUIDs, cross-refs, etc.
    SetMetadata --> SendToChannel: client.WARCWriter
    SendToChannel --> [*]
```

**Metadata Extraction Points:**

| Metadata | Source | Location |
|----------|--------|----------|
| WARC-Target-URI | HTTP request line + Host header | client.go:679 |
| WARC-Date | Capture timestamp | warc.go:165 |
| WARC-Record-ID | UUID generation | client.go:735 |
| WARC-Concurrent-To | Cross-reference to paired record | client.go:740 |
| WARC-IP-Address | conn.RemoteAddr() | dialer.go:765 |
| WARC-Payload-Digest | Hash of response body | client.go:693 |
| WARC-Block-Digest | Hash of entire record | client.go:720 |
| Content-Type | HTTP response header | client.go:656 |
| Content-Length | Record content size | warc.go:169 |

### 4. Deduplication Subsystem

**Purpose:** Avoid storing duplicate content

**Components:**
- Local hash table (`sync.Map`)
- Doppelganger API client
- CDX API client

**Deduplication Decision Tree:**

```mermaid
flowchart TD
    START[Response parsed]

    SIZE_CHECK{Payload size >=<br/>threshold?}
    START --> SIZE_CHECK

    SIZE_CHECK -->|No| SKIP[Skip deduplication<br/>Store full record]
    SIZE_CHECK -->|Yes| EMPTY_CHECK

    EMPTY_CHECK{Is empty payload?<br/>Known empty digest}
    EMPTY_CHECK -->|Yes| SKIP
    EMPTY_CHECK -->|No| LOCAL_CHECK

    LOCAL_CHECK[Check local<br/>dedupeHashTable]

    LOCAL_CHECK --> LOCAL_FOUND{Found locally?}

    LOCAL_FOUND -->|Yes| CREATE_REVISIT[Create revisit record<br/>WARC-Type: revisit<br/>WARC-Refers-To: UUID<br/>WARC-Truncated: length]
    LOCAL_FOUND -->|No| DOPPEL_ENABLED

    DOPPEL_ENABLED{Doppelganger<br/>enabled?}

    DOPPEL_ENABLED -->|Yes| DOPPEL_QUERY[HTTP GET to<br/>doppelganger API]
    DOPPEL_ENABLED -->|No| CDX_ENABLED

    DOPPEL_QUERY --> DOPPEL_FOUND{Found?}

    DOPPEL_FOUND -->|Yes| CREATE_DOPPEL_REVISIT[Create revisit record<br/>WARC-Refers-To-Target-URI<br/>WARC-Refers-To-Date]
    DOPPEL_FOUND -->|No| CDX_ENABLED

    CDX_ENABLED{CDX enabled?}

    CDX_ENABLED -->|Yes| CDX_QUERY[HTTP GET to<br/>CDX API]
    CDX_ENABLED -->|No| STORE_FULL

    CDX_QUERY --> CDX_FOUND{Found?}

    CDX_FOUND -->|Yes| CREATE_CDX_REVISIT[Create revisit record<br/>WARC-Refers-To-Target-URI<br/>WARC-Refers-To-Date]
    CDX_FOUND -->|No| STORE_FULL

    STORE_FULL[Store full record<br/>Add to dedupeHashTable]

    CREATE_REVISIT --> UPDATE_LOCAL_STATS[Increment:<br/>local_deduped_total<br/>local_deduped_bytes_total]

    CREATE_DOPPEL_REVISIT --> UPDATE_DOPPEL_STATS[Increment:<br/>doppelganger_deduped_total<br/>doppelganger_deduped_bytes_total]

    CREATE_CDX_REVISIT --> UPDATE_CDX_STATS[Increment:<br/>cdx_deduped_total<br/>cdx_deduped_bytes_total]

    UPDATE_LOCAL_STATS --> END[Send to WARC writer]
    UPDATE_DOPPEL_STATS --> END
    UPDATE_CDX_STATS --> END
    STORE_FULL --> END
    SKIP --> END

    style LOCAL_CHECK fill:#e8f5e9
    style DOPPEL_QUERY fill:#e3f2fd
    style CDX_QUERY fill:#f3e5f5
    style CREATE_REVISIT fill:#fff9c4
    style UPDATE_LOCAL_STATS fill:#ffebee
```

**Revisit Record Structure:**

```
Normal Response Record:
WARC-Type: response
WARC-Record-ID: <urn:uuid:NEW-UUID>
WARC-Target-URI: https://example.com/page
WARC-Payload-Digest: sha1:ABC123...
Content-Length: 50000
[Full HTTP response with body]

↓ Becomes (if duplicate found) ↓

Revisit Record:
WARC-Type: revisit
WARC-Record-ID: <urn:uuid:NEW-UUID>
WARC-Target-URI: https://example.com/page
WARC-Refers-To-Target-URI: https://example.com/page
WARC-Refers-To-Date: 2025-01-20T10:30:00Z
WARC-Refers-To: <urn:uuid:ORIGINAL-UUID>  (if local)
WARC-Payload-Digest: sha1:ABC123...
WARC-Truncated: length
WARC-Profile: http://netpreserve.org/warc/1.1/revisit/identical-payload-digest
Content-Length: 250
[HTTP response headers only, body truncated]
```

### 5. WARC Writing Subsystem

**Purpose:** Persist records to disk with compression and rotation

**Components:**
- RecordBatch channel (buffered)
- Pool of recordWriter goroutines
- File rotation logic
- Compression (GZIP/ZSTD)

**Writer Pool Architecture:**

```mermaid
graph TD
    subgraph "Client"
        CH[WARCWriter Channel<br/>buffered: 1000]
    end

    subgraph "Writer Pool"
        W1[Writer Goroutine 1]
        W2[Writer Goroutine 2]
        W3[Writer Goroutine 3]
        W4[Writer Goroutine 4]
    end

    subgraph "WARC Files"
        F1[WARC-...-00001.warc.gz]
        F2[WARC-...-00002.warc.gz]
        F3[WARC-...-00003.warc.gz]
        F4[WARC-...-00004.warc.gz]
    end

    CH -->|RecordBatch| W1
    CH -->|RecordBatch| W2
    CH -->|RecordBatch| W3
    CH -->|RecordBatch| W4

    W1 -->|Write, rotate| F1
    W2 -->|Write, rotate| F2
    W3 -->|Write, rotate| F3
    W4 -->|Write, rotate| F4

    style CH fill:#e8f5e9
    style W1 fill:#e3f2fd
    style W2 fill:#e3f2fd
    style W3 fill:#e3f2fd
    style W4 fill:#e3f2fd
```

**File Rotation State Machine:**

```mermaid
stateDiagram-v2
    [*] --> Initialize
    Initialize --> GenerateFilename: Serial counter
    GenerateFilename --> CheckExists: File exists?
    CheckExists --> IncrementSerial: Yes
    IncrementSerial --> GenerateFilename
    CheckExists --> CreateFile: No
    CreateFile --> AddOpenSuffix: Add ".open" suffix
    AddOpenSuffix --> OpenWriter: Open file for writing
    OpenWriter --> WriteWarcinfo: Write warcinfo record
    WriteWarcinfo --> WaitingForBatch

    WaitingForBatch --> CheckSize: RecordBatch received

    state CheckSize <<choice>>
    CheckSize --> WriteBatch: Size OK
    CheckSize --> Rotate: Size exceeded

    Rotate --> Flush: Flush buffers
    Flush --> CloseCompressor: Close gzip/zstd
    CloseCompressor --> CloseFile
    CloseFile --> Rename: Remove ".open" suffix
    Rename --> GenerateFilename

    WriteBatch --> WriteRecords: For each record
    WriteRecords --> UpdateMetrics: total_data_written
    UpdateMetrics --> SignalFeedback: Optional feedback
    SignalFeedback --> WaitingForBatch

    WaitingForBatch --> Shutdown: Channel closed
    Shutdown --> Flush
    Shutdown --> [*]
```

**Compression Pipeline:**

```mermaid
graph LR
    subgraph "Record Content"
        CONTENT[SpooledTempFile<br/>Uncompressed data]
    end

    subgraph "Writer Layers"
        RECORD_BUF[Record bufio.Writer<br/>4KB buffer]
        COMPRESSOR[GZIP/ZSTD Encoder<br/>Compression block]
        FILE_BUF[File bufio.Writer<br/>64KB buffer]
        FILE[os.File<br/>Disk I/O]
    end

    CONTENT -->|io.Copy| RECORD_BUF
    RECORD_BUF -->|Flush per record| COMPRESSOR
    COMPRESSOR -->|Compressed chunks| FILE_BUF
    FILE_BUF -->|Periodic flush| FILE

    style CONTENT fill:#e3f2fd
    style COMPRESSOR fill:#fff9c4
    style FILE fill:#e8f5e9
```

### 6. Observability Subsystem

**Purpose:** Provide metrics and logging for monitoring and debugging

**Components:**
- StatsRegistry interface (Counters, Gauges, Histograms)
- LogBackend interface (Debug, Info, Warn, Error)

**Metrics Collection Points:**

```mermaid
graph TB
    subgraph "Data Path Metrics"
        M1[total_data_written<br/>Type: Counter<br/>Location: Writer]
    end

    subgraph "Deduplication Metrics"
        M2[local_deduped_total<br/>Type: Counter<br/>Location: Dedupe]
        M3[local_deduped_bytes_total<br/>Type: Counter<br/>Location: Dedupe]
        M4[doppelganger_deduped_total<br/>Type: Counter<br/>Location: Dedupe]
        M5[doppelganger_deduped_bytes_total<br/>Type: Counter<br/>Location: Dedupe]
        M6[cdx_deduped_total<br/>Type: Counter<br/>Location: Dedupe]
        M7[cdx_deduped_bytes_total<br/>Type: Counter<br/>Location: Dedupe]
    end

    subgraph "Proxy Metrics"
        M8[proxy_requests_total<br/>Type: Counter<br/>Labels: proxy<br/>Location: Dialer]
        M9[proxy_errors_total<br/>Type: Counter<br/>Labels: proxy<br/>Location: Dialer]
        M10[proxy_last_used_nanoseconds<br/>Type: Gauge<br/>Labels: proxy<br/>Location: Dialer]
    end

    subgraph "StatsRegistry Implementation"
        IMPL[User-provided or<br/>Local Registry]
    end

    M1 --> IMPL
    M2 --> IMPL
    M3 --> IMPL
    M4 --> IMPL
    M5 --> IMPL
    M6 --> IMPL
    M7 --> IMPL
    M8 --> IMPL
    M9 --> IMPL
    M10 --> IMPL

    style M1 fill:#e3f2fd
    style M2 fill:#fff9c4
    style M8 fill:#ffebee
    style IMPL fill:#e8f5e9
```

**Logging Event Hierarchy:**

```mermaid
graph TB
    subgraph "Debug Events"
        D1[Proxy selected<br/>dialer.go:293]
        D2[DNS resolved<br/>dns.go:44]
        D3[Connection established<br/>dialer.go:473]
        D4[TLS connection established<br/>dialer.go:594]
        D5[WARC record written<br/>write.go:145]
    end

    subgraph "Info Events"
        I1[WARC file created<br/>warc.go:132]
        I2[WARC file rotation<br/>warc.go:152]
        I3[WARC writer shutdown<br/>warc.go:224]
    end

    subgraph "Error Events"
        E1[DNS resolution failed<br/>dns.go:52]
        E2[Proxy connection failed<br/>dialer.go:456]
        E3[Direct connection failed<br/>dialer.go:483]
        E4[TLS handshake failed<br/>dialer.go:575]
        E5[WARC record write failed<br/>write.go:139]
        E6[DiscardHook rejection<br/>client.go:671]
        E7[Digest calculation failed<br/>client.go:705]
    end

    subgraph "LogBackend"
        LOG[User-provided or<br/>No-op Logger]
    end

    D1 --> LOG
    D2 --> LOG
    D3 --> LOG
    D4 --> LOG
    D5 --> LOG
    I1 --> LOG
    I2 --> LOG
    I3 --> LOG
    E1 --> LOG
    E2 --> LOG
    E3 --> LOG
    E4 --> LOG
    E5 --> LOG
    E6 --> LOG
    E7 --> LOG

    style D1 fill:#e3f2fd
    style I1 fill:#e8f5e9
    style E1 fill:#ffebee
    style LOG fill:#fff9c4
```

### 7. Storage Subsystem

**Purpose:** Efficient temporary storage with RAM/disk threshold

**Component:**
- `SpooledTempFile` - Automatic RAM → disk spillover

**SpooledTempFile State Machine:**

```mermaid
stateDiagram-v2
    [*] --> InMemory: Create()

    state InMemory {
        [*] --> BytesBuffer
        BytesBuffer --> CheckSize: Write()
        CheckSize --> BytesBuffer: Size < threshold
    }

    CheckSize --> SpillToDisk: Size >= threshold

    state SpillToDisk {
        [*] --> CreateTempFile: os.CreateTemp()
        CreateTempFile --> CopyBuffer: io.Copy(file, buffer)
        CopyBuffer --> FreeBuffer: buffer = nil (GC)
    }

    SpillToDisk --> OnDisk

    state OnDisk {
        [*] --> FileOps
        FileOps --> FileOps: Read/Write/Seek
    }

    OnDisk --> Cleanup: Close()
    InMemory --> Cleanup: Close()

    Cleanup --> RemoveFile: os.Remove() if on disk
    RemoveFile --> [*]

    note right of InMemory
        Threshold = MaxRAMUsageFraction
        of system RAM or FullOnDisk=true
        forces immediate disk spooling
    end note
```

**Memory Pressure Management:**

```mermaid
flowchart TD
    START[Record content being written]

    CHECK_MODE{FullOnDisk<br/>enabled?}
    START --> CHECK_MODE

    CHECK_MODE -->|Yes| DISK[Immediately create<br/>temp file on disk]
    CHECK_MODE -->|No| RAM_CHECK

    RAM_CHECK[Calculate threshold:<br/>sysRAM * MaxRAMUsageFraction]

    RAM_CHECK --> WRITE_MEM[Write to in-memory<br/>bytes.Buffer]

    WRITE_MEM --> SIZE_CHECK{Buffer size ><br/>threshold?}

    SIZE_CHECK -->|No| WRITE_MEM
    SIZE_CHECK -->|Yes| SPILL[Spill to disk:<br/>1. Create temp file<br/>2. Copy buffer<br/>3. Free memory]

    SPILL --> WRITE_DISK
    DISK --> WRITE_DISK[Write to disk file]

    WRITE_DISK --> COMPLETE[Content complete]

    style RAM_CHECK fill:#e3f2fd
    style WRITE_MEM fill:#e8f5e9
    style SPILL fill:#fff9c4
    style WRITE_DISK fill:#ffebee
```

## Inter-Subsystem Communication

### Message Passing

```mermaid
graph LR
    subgraph "Goroutines"
        APP[Application]
        DIAL[Dialer]
        WRAP[writeWARCFromConnection]
        REQ[readRequest]
        RESP[readResponse]
        WRITE[recordWriter Pool]
    end

    subgraph "Channels"
        TARGET[targetURI chan]
        RECORD[record chan]
        BATCH[RecordBatch chan]
        FEEDBACK[feedback chan]
    end

    APP -->|synchronous| DIAL
    DIAL -->|spawn| WRAP
    WRAP -->|spawn| REQ
    WRAP -->|spawn| RESP

    REQ -->|async| TARGET
    TARGET -->|async| RESP

    REQ -->|async| RECORD
    RESP -->|async| RECORD

    RECORD -->|collect| WRAP
    WRAP -->|async| BATCH

    BATCH -->|load balanced| WRITE
    WRITE -->|optional| FEEDBACK
    FEEDBACK -->|sync| APP

    style TARGET fill:#e3f2fd
    style RECORD fill:#fff9c4
    style BATCH fill:#e8f5e9
    style FEEDBACK fill:#ffebee
```

### Synchronization Primitives

| Primitive | Usage | Location |
|-----------|-------|----------|
| `sync.WaitGroup` | Wait for all active requests | CustomHTTPClient.WaitGroup |
| `sync.Map` | Thread-safe deduplication table | CustomHTTPClient.dedupeHashTable |
| `atomic.Uint32` | Lock-free proxy round-robin | customDialer.proxyRoundRobinIndex |
| `sync.Once` | One-time read deadline set | CustomConnection.firstRead |
| `sync.Mutex` | Protect testLogger entries | testLogger.mu |
| Buffered channels | Async batch submission | WARCWriter (buffer: 1000) |
| Unbuffered channels | Sync metadata passing | targetURICh, recordChan |
| Optional feedback chan | Synchronous WARC write | RecordBatch.FeedbackChan |

### Context Propagation

```mermaid
graph TD
    START[Application creates context]

    START --> CTX1[context.Background]

    CTX1 --> ADD_TIMEOUT[Add timeout:<br/>ctx, cancel = context.WithTimeout]

    ADD_TIMEOUT --> ADD_PROXY[Add proxy type:<br/>warc.WithProxyType]

    ADD_PROXY --> ADD_FEEDBACK[Add feedback channel:<br/>warc.WithFeedbackChannel]

    ADD_FEEDBACK --> ADD_CONN[Add connection channel:<br/>warc.WithWrappedConnection]

    ADD_CONN --> USE_CTX[req = req.WithContext(ctx)]

    USE_CTX --> PROP1[client.Do<br/>→ transport.RoundTrip]

    PROP1 --> PROP2[→ dialer.CustomDialContext]

    PROP2 --> PROP3[→ wrapConnection]

    PROP3 --> PROP4[→ writeWARCFromConnection]

    PROP4 --> EXTRACT[Extract context values:<br/>- ProxyType<br/>- FeedbackChan<br/>- ConnChan]

    EXTRACT --> USE[Use values to control:<br/>- Proxy selection<br/>- Synchronous writes<br/>- Connection inspection]

    style ADD_TIMEOUT fill:#ffebee
    style ADD_PROXY fill:#e3f2fd
    style ADD_FEEDBACK fill:#e8f5e9
    style EXTRACT fill:#fff9c4
```

## Advanced Features

### 1. Synchronous WARC Writes

**Default (Async):**
```go
resp, _ := client.Do(req)
io.Copy(io.Discard, resp.Body)
resp.Body.Close()
// WARC writing happens in background
// Returns immediately
```

**Synchronous:**
```go
feedbackChan := make(chan struct{})
ctx := warc.WithFeedbackChannel(context.Background(), feedbackChan)
req = req.WithContext(ctx)

resp, _ := client.Do(req)
io.Copy(io.Discard, resp.Body)
resp.Body.Close()

<-feedbackChan  // Blocks until WARC written to disk
```

### 2. Connection Inspection

```go
connChan := make(chan *warc.CustomConnection, 1)
ctx := warc.WithWrappedConnection(context.Background(), connChan)
req = req.WithContext(ctx)

resp, _ := client.Do(req)

// Access wrapped connection
wrappedConn := <-connChan
realConn := wrappedConn.Conn  // Underlying net.Conn
// Can inspect TLS state, remote address, etc.
```

### 3. Random Local IP (IPv6)

**Purpose:** Avoid rate limiting by varying source IP

```go
client, _ := warc.NewWARCWritingHTTPClient(warc.HTTPClientSettings{
    RandomLocalIP: true,
    IPv6AnyIP:     true,  // Use ::/64 prefix from available interfaces
})
```

**How it works:**
1. Goroutine watches network interfaces (polls every 10s)
2. Detects IPv6 addresses with /64 prefix
3. For each request:
   - Generates random IPv6 in detected subnets
   - Sets as `LocalAddr` on dialer
   - Each request uses different source IP

### 4. Custom TLS Fingerprinting

Uses `refraction-networking/utls` to mimic real browsers:

```go
func getCustomTLSSpec() utls.ClientHelloID {
    return utls.HelloChrome_120  // Looks like Chrome 120
}
```

Avoids TLS-based blocking/fingerprinting.

## Error Handling Patterns

### Network Errors

```go
// Proxy connection failed
if err != nil {
    logBackend.Error("proxy connection failed",
        "proxy", selectedProxy.name,
        "address", address,
        "error", err)
    statsRegistry.Counter("proxy_errors_total",
        label.String("proxy", selectedProxy.name)).Inc()

    // Try direct connection if allowed
    if allowDirectFallback {
        return directConnection()
    }
    return nil, err
}
```

### WARC Writing Errors

```go
if err := writer.WriteRecord(record); err != nil {
    logBackend.Error("failed to write WARC record content",
        "file", writer.FileName,
        "error", err)
    // Continue to next record (don't crash)
}
```

### Graceful Degradation

- DNS failure → Return error (don't archive)
- Proxy failure → Try direct (if allowed)
- Dedupe API failure → Store full record
- Digest calculation error → Log and skip digest
- DiscardHook error → Log and keep record

## Performance Optimizations

### 1. Concurrent DNS Queries

```go
// Sequential (dnsConcurrency = 1)
for _, server := range dnsServers {
    result, err := query(server)
    if err == nil { return result }
}

// Parallel (dnsConcurrency = -1)
results := make(chan result, len(dnsServers))
for _, server := range dnsServers {
    go func(s string) {
        results <- query(s)
    }(server)
}
// Early termination when both A and AAAA found
```

### 2. Writer Pool Parallelism

```go
// Single writer (WARCWriterPoolSize = 1)
// Bottleneck: One file open at a time

// Multiple writers (WARCWriterPoolSize = 4)
// Each writer has its own file
// 4x throughput (if not I/O bound)
```

### 3. Buffered Channels

```go
// Unbuffered (bad)
WARCWriter = make(chan *RecordBatch)
// Producer blocks until consumer ready

// Buffered (good)
WARCWriter = make(chan *RecordBatch, 1000)
// Producer continues until buffer full
// Smooths burst traffic
```

### 4. Atomic Operations

```go
// Lock-based (slower)
mu.Lock()
index = (index + 1) % len(proxies)
mu.Unlock()

// Atomic (faster)
index := d.proxyRoundRobinIndex.Add(1) % uint32(len(proxies))
```

## Summary

The gowarc library is built on several key architectural principles:

1. **Separation of Concerns**: Each subsystem has a clear responsibility
2. **Concurrent Design**: Goroutines + channels for parallelism
3. **Pluggable Interfaces**: Stats, logging, deduplication can be customized
4. **Resource Efficiency**: Spooled temp files, connection pooling, caching
5. **Observability**: Comprehensive metrics and structured logging
6. **Fault Tolerance**: Graceful error handling, fallback mechanisms
7. **Performance**: Parallel DNS, writer pools, atomic operations

Any software engineer reviewing these diagrams should understand:
- How requests flow from application to WARC file
- Where proxies and DNS resolution fit in
- How byte capture works (pipes + goroutines)
- When and how deduplication occurs
- The role of each subsystem
- How to customize behavior via interfaces
