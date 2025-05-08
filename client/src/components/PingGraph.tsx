import React, { useState } from 'react';
import { 
  LineChart, 
  Line, 
  XAxis, 
  YAxis, 
  CartesianGrid, 
  Tooltip, 
  Legend, 
  ResponsiveContainer,
  Scatter
} from 'recharts';
import type { PingStats } from '../services/api';

interface PingGraphProps {
  stats: PingStats;
  onClose: () => void;
}

interface GraphDataPoint {
  index: number;
  rtt: number;
  min: number;
  avg: number;
  max: number;
  hasError: boolean;
  errorMessage?: string;
}

const PingGraph: React.FC<PingGraphProps> = ({ stats, onClose }) => {
  const [showMin, setShowMin] = useState<boolean>(true);
  const [showAvg, setShowAvg] = useState<boolean>(true);
  const [showMax, setShowMax] = useState<boolean>(true);
  const [showRTT, setShowRTT] = useState<boolean>(true);
  const [showErrors, setShowErrors] = useState<boolean>(true);

  // Prepare data for the graph including error events
  const prepareGraphData = (): GraphDataPoint[] => {
    if (!stats.SuccessRTT || stats.SuccessRTT.length === 0) {
      return [];
    }

    const dataPoints = stats.SuccessRTT.map((rtt, index) => ({
      index,
      rtt,
      min: stats.MinRTT,
      avg: stats.AvgRTT,
      max: stats.MaxRTT,
      hasError: false,
      errorMessage: ''
    }));

    // Add error markers if available
    if (stats.errorEvents && stats.errorEvents.length > 0) {
      // Create a map to estimate ping index from timestamp
      // This is a rough approximation since we don't have exact indices for errors
      const startTime = new Date(stats.StartTime).getTime();
      const lastUpdateTime = new Date(stats.LastUpdate).getTime();
      const timeRange = lastUpdateTime - startTime;
      
      // Add error information to data points
      stats.errorEvents.forEach(error => {
        const errorTime = new Date(error.timestamp).getTime();
        // Calculate the approximate position in the ping sequence
        const timeFraction = (errorTime - startTime) / timeRange;
        const estimatedIndex = Math.floor(timeFraction * (dataPoints.length - 1));
        
        // Only add if the estimated index is in valid range
        if (estimatedIndex >= 0 && estimatedIndex < dataPoints.length) {
          dataPoints[estimatedIndex] = {
            ...dataPoints[estimatedIndex],
            hasError: true,
            errorMessage: error.message
          };
        }
      });
    }

    return dataPoints;
  };

  const data = prepareGraphData();

  if (data.length === 0) {
    return (
      <div className="ping-graph-container">
        <div className="ping-graph-header">
          <h3 className="ping-graph-title">Ping Statistics for {stats.Host}</h3>
          <button className="ping-graph-close" onClick={onClose}>&times;</button>
        </div>
        <p>No successful ping data available for this host.</p>
      </div>
    );
  }

  return (
    <div className="ping-graph-container">
      <div className="ping-graph-header">
        <h3 className="ping-graph-title">Ping Statistics for {stats.Host}</h3>
        <button className="ping-graph-close" onClick={onClose}>&times;</button>
      </div>
      
      <div className="ping-graph-controls">
        <div className="graph-control-item">
          <input
            type="checkbox"
            id="show-min"
            className="graph-control-checkbox"
            checked={showMin}
            onChange={() => setShowMin(!showMin)}
          />
          <label htmlFor="show-min">Show Min RTT</label>
        </div>
        
        <div className="graph-control-item">
          <input
            type="checkbox"
            id="show-avg"
            className="graph-control-checkbox"
            checked={showAvg}
            onChange={() => setShowAvg(!showAvg)}
          />
          <label htmlFor="show-avg">Show Avg RTT</label>
        </div>
        
        <div className="graph-control-item">
          <input
            type="checkbox"
            id="show-max"
            className="graph-control-checkbox"
            checked={showMax}
            onChange={() => setShowMax(!showMax)}
          />
          <label htmlFor="show-max">Show Max RTT</label>
        </div>
        
        <div className="graph-control-item">
          <input
            type="checkbox"
            id="show-rtt"
            className="graph-control-checkbox"
            checked={showRTT}
            onChange={() => setShowRTT(!showRTT)}
          />
          <label htmlFor="show-rtt">Show RTT Values</label>
        </div>
        
        <div className="graph-control-item">
          <input
            type="checkbox"
            id="show-errors"
            className="graph-control-checkbox"
            checked={showErrors}
            onChange={() => setShowErrors(!showErrors)}
          />
          <label htmlFor="show-errors">Show Errors</label>
        </div>
      </div>
      
      <ResponsiveContainer width="100%" height={300}>
        <LineChart
          data={data}
          margin={{
            top: 5,
            right: 30,
            left: 20,
            bottom: 5,
          }}
        >
          <CartesianGrid strokeDasharray="3 3" />
          <XAxis dataKey="index" label={{ value: 'Ping Sequence', position: 'insideBottomRight', offset: -10 }} />
          <YAxis label={{ value: 'RTT (ms)', angle: -90, position: 'insideLeft' }} />
          <Tooltip 
            content={({ active, payload }) => {
              if (active && payload && payload.length) {
                return (
                  <div className="graph-tooltip">
                    <p>Ping #{payload[0].payload.index}</p>
                    {showRTT && <p>RTT: {payload[0].payload.rtt.toFixed(2)} ms</p>}
                    {showMin && <p>Min: {payload[0].payload.min.toFixed(2)} ms</p>}
                    {showAvg && <p>Avg: {payload[0].payload.avg.toFixed(2)} ms</p>}
                    {showMax && <p>Max: {payload[0].payload.max.toFixed(2)} ms</p>}
                    {payload[0].payload.hasError && (
                      <p className="error-tooltip">Error: {payload[0].payload.errorMessage || 'Timeout'}</p>
                    )}
                  </div>
                );
              }
              return null;
            }}
          />
          <Legend />
          {showRTT && <Line type="linear" dataKey="rtt" stroke="#8884d8" activeDot={{ r: 6 }} name="RTT Values" isAnimationActive={false} />}
          {showMin && <Line type="linear" dataKey="min" stroke="#82ca9d" strokeDasharray="5 5" name="Min RTT" isAnimationActive={false} />}
          {showAvg && <Line type="linear" dataKey="avg" stroke="#ff7300" strokeDasharray="5 5" name="Avg RTT" isAnimationActive={false} />}
          {showMax && <Line type="linear" dataKey="max" stroke="#ff0000" strokeDasharray="5 5" name="Max RTT" isAnimationActive={false} />}
          
          {/* Error visualization */}
          {showErrors && (
            <Scatter
              name="Errors"
              dataKey={(entry) => entry.hasError ? entry.max * 1.1 : null} // Position slightly above the max line
              shape="cross"
              legendType="none"
              fill="#d32f2f"
              stroke="#d32f2f"
              strokeWidth={2}
              isAnimationActive={false}
            />
          )}
        </LineChart>
      </ResponsiveContainer>
      
      <div className="ping-stats-summary">
        <p>Total Sent: {stats.Sent} | Received: {stats.Received} | Loss: {(stats.Loss * 100).toFixed(2)}%</p>
      </div>
    </div>
  );
};

export default PingGraph;
