# PingMon
A real-time ping monitoring and visualization tool with a Go backend and React frontend.

## Overview

PingMon is a web-based application that allows users to monitor the ping statistics of various hosts. It provides real-time ping data visualization with interactive charts.

### Features

- **Multi-Host Monitoring**: Track multiple hosts simultaneously
- **Real-Time Data**: Live updates of ping statistics
- **Interactive Charts**: Visualize ping performance with line charts
- **Customizable Views**: Toggle between different metrics (RTT, Min, Avg, Max)
- **Actions**: Reset statistics or remove hosts from monitoring

## Project Structure

The application is divided into two main components:

- **Server**: Go backend that handles ICMP ping operations
- **Client**: React/Vite frontend for visualization and user interaction

## Requirements

- **Go 1.18+**
- **Node.js 16+**
- **Root/Administrator privileges** (required for ICMP operations)

## Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/7c/pingmon.git
   cd pingmon
   ```

2. Install server dependencies:
   ```bash
   cd server
   go mod tidy
   ```

3. Install client dependencies:
   ```bash
   cd ../client
   npm install
   ```

## Building

1. Build the client:
   ```bash
   cd client
   npm run build
   ```

   This will compile the React application and place the build files in the `server/build` directory.

## Running

The application requires root/administrator privileges to use ICMP ping:

```bash
cd server
sudo go run main.go
```

Once running, access the application at: http://localhost:6868

You can customize the server port using the `--port` flag:

```bash
cd server
sudo go run main.go --port 8888
```

## Documentation

For more detailed information:

- [Server Documentation](./server/README.md)
- [Client Documentation](./client/README.md)

## License

[MIT License](LICENSE)
