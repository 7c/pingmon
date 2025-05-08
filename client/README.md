# PingMon Frontend

The frontend client for the PingMon application built with React, TypeScript, and Vite.

## Overview

This is the client-side component of PingMon, providing a user interface for monitoring and visualizing ping statistics. It communicates with the Go backend server to fetch real-time data and display it in an interactive table and graph format.

## Features

- **Host Management**: Add new hosts to monitor
- **Interactive Table**: View ping statistics in a sortable table
- **Visual Graphs**: Visualize ping performance over time
- **Multiple Hosts**: Toggle between different hosts and view multiple graphs simultaneously
- **Custom Controls**: Reset statistics or remove hosts from monitoring

## Architecture

The client is built with the following architecture:

- **React**: UI framework
- **TypeScript**: Type safety
- **Vite**: Build tool
- **Recharts**: Data visualization library for graphs
- **CSS Modules**: Scoped styling

## Key Components

- **App.tsx**: Main application container
- **AddPinger.tsx**: Form for adding new hosts to monitor
- **PingTable.tsx**: Interactive table displaying ping statistics
- **PingGraph.tsx**: Chart visualization of ping data
- **API Service**: Communication layer with the backend

## Development

### Prerequisites

- **Node.js 16+**
- **npm/yarn**

### Installation

```bash
npm install
```

### Running in Development Mode

```bash
npm run dev
```

This starts the development server with hot module replacement.

### Building for Production

```bash
npm run build
```

This compiles the application and outputs to the `../server/build` directory, ready to be served by the Go backend.

## API Integration

The client communicates with the server using the following API endpoints:

- `GET /api/pinger`: Fetch statistics for all running pingers
- `POST /api/pinger/add`: Add new IPs to monitor
- `POST /api/pinger/reset`: Reset statistics for specific IPs
- `POST /api/pinger/remove`: Remove pingers completely

## UI Design

The UI is designed with simplicity and clarity in mind, featuring:

- Clean, minimal layout
- Responsive design for various screen sizes
- High contrast for readability
- Interactive elements with clear feedback
- Non-animated graphs for distraction-free monitoring
