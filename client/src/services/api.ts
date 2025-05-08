import axios from 'axios';

export interface ErrorEvent {
  timestamp: string;
  message: string;
}

export interface PingStats {
  Host: string;
  IPAddr: {
    IP: string;
  };
  StartTime: string;
  LastUpdate: string;
  Sent: number;
  Received: number;
  Loss: number;
  MinRTT: number;
  MaxRTT: number;
  AvgRTT: number;
  StdDevRTT: number;
  SuccessRTT: number[];
  ErrorsCount: number;
  Errors: string[];
  Running: boolean;
  errorEvents: ErrorEvent[]; // New field for error events with timestamps
}

export interface PingStatsResponse {
  [ip: string]: PingStats;
}

// Helper function to convert nanoseconds to milliseconds
const nsToMs = (ns: number): number => ns / 1000000;

const api = {
  // Add new IPs to monitor
  addPingers: async (ips: string[]): Promise<{ success: boolean; errors?: Record<string, string> }> => {
    const response = await axios.post('/api/pinger/add', { ips });
    return response.data;
  },

  // Get stats for all pingers
  getPingerStats: async (): Promise<PingStatsResponse> => {
    const response = await axios.get('/api/pinger');
    
    // Convert nanosecond RTT values to milliseconds for display
    const data = response.data;
    Object.keys(data).forEach(ip => {
      const stats = data[ip];
      stats.MinRTT = nsToMs(stats.MinRTT);
      stats.MaxRTT = nsToMs(stats.MaxRTT);
      stats.AvgRTT = nsToMs(stats.AvgRTT);
      stats.StdDevRTT = nsToMs(stats.StdDevRTT);
      
      // Also convert each RTT value in the array if needed
      if (stats.SuccessRTT && stats.SuccessRTT.length > 0) {
        stats.SuccessRTT = stats.SuccessRTT.map(nsToMs);
      }
    });
    
    return data;
  },

  // Reset stats for specific IPs
  resetPingers: async (ips: string[]): Promise<{ success: boolean }> => {
    const response = await axios.post('/api/pinger/reset', { ips });
    return response.data;
  },

  // Remove pingers completely
  removePingers: async (ips: string[]): Promise<{ success: boolean }> => {
    const response = await axios.post('/api/pinger/remove', { ips });
    return response.data;
  }
};

export default api;
