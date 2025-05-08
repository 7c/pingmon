# PingMon Server

The backend server component of the PingMon application.

## Overview

The server is a Go application that handles ICMP ping operations to multiple hosts simultaneously and provides a RESTful API for the frontend. It manages ping operations, collects statistics, and serves the React frontend as a static single-page application.

## Architecture

The server uses a manager pattern to handle multiple ping operations:

- **PingerManager**: Coordinates all active ping operations
- **Pinger Class**: Encapsulates ping functionality for a single host
- **REST API**: Provides endpoints for frontend interactions
- **Static File Server**: Serves the compiled React frontend

## Prerequisites

- **Go 1.18+** 
- **Root/Administrator privileges** (required for ICMP operations)
- The client application must be built and its output placed in the `build` directory

## API Endpoints

### GET /api/pinger
Returns statistics for all running pingers.

**Response:**
```json
{
  "192.168.1.1": {
    "Host": "192.168.1.1",
    "Running": true,
    "Sent": 10,
    "Received": 10,
    "Loss": 0,
    "MinRTT": 1.23,
    "AvgRTT": 2.45,
    "MaxRTT": 3.67,
    "LastUpdate": "2023-06-01T12:34:56Z"
  }
}
```

### POST /api/pinger/add
Adds new IPs to monitor.

**Request:**
```json
{
  "ips": ["192.168.1.1", "8.8.8.8"]
}
```

**Response:**
```json
{
  "success": true
}
```

### POST /api/pinger/reset
Resets statistics for specific IPs.

**Request:**
```json
{
  "ips": ["192.168.1.1"]
}
```

**Response:**
```json
{
  "success": true
}
```

### POST /api/pinger/remove
Removes pingers completely.

**Request:**
```json
{
  "ips": ["192.168.1.1"]
}
```

**Response:**
```json
{
  "success": true
}
```

## Starting the Server

The server requires root/administrator privileges to use ICMP ping:

```bash
sudo go run main.go
```

By default, the server listens on port 6868. You can access the web interface at http://localhost:6868.

### Command-Line Options

You can customize the server port using the `--port` flag:

```bash
sudo go run main.go --port 8888
```

## Configuration

The server uses the following default configuration:

- **Port**: 6868 (customizable via `--port` flag)
- **Static Files Directory**: `build`
- **Ping Interval**: 1 second
- **Number of Pings**: Continuous until stopped

## Security Considerations

The application requires root/administrator privileges due to the use of ICMP ping. This is a standard requirement for applications using raw sockets. The application is designed for internal network use and does not implement authentication or authorization.

## Logging

The server logs all ping operations, including successes and failures, along with their RTT statistics. All API requests are also logged with their response time.
