import React, { Fragment } from 'react';
import type { PingStats } from '../services/api';
import PingGraph from './PingGraph';

interface PingTableProps {
  stats: Record<string, PingStats>;
  onReset: (ips: string[]) => void;
  onRemove: (ips: string[]) => void;
  onSelectHost: (ip: string) => void;
  selectedHosts: string[];
}

const PingTable: React.FC<PingTableProps> = ({ stats, onReset, onRemove, onSelectHost, selectedHosts }) => {
  // Format milliseconds to display in a readable format
  const formatTime = (timeMs: number): string => {
    if (timeMs < 1) return '< 1ms';
    return `${timeMs.toFixed(2)}ms`;
  };
  
  // Convert ping loss to percentage with 2 decimal places
  const formatLoss = (loss: number): string => {
    return `${(loss * 100).toFixed(2)}%`;
  };

  // Format the timestamp
  const formatDate = (timestamp: string): string => {
    const date = new Date(timestamp);
    return new Intl.DateTimeFormat('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }).format(date);
  };

  // Handle reset for a single IP
  const handleReset = (ip: string, e: React.MouseEvent) => {
    e.stopPropagation(); // Prevent row click
    onReset([ip]);
  };

  // Handle remove for a single IP
  const handleRemove = (ip: string, e: React.MouseEvent) => {
    e.stopPropagation(); // Prevent row click
    if (window.confirm(`Are you sure you want to remove ${ip}?`)) {
      onRemove([ip]);
    }
  };

  // Handle row click to show details/graph
  const handleRowClick = (ip: string) => {
    onSelectHost(ip);
  };

  // If no stats are available
  if (Object.keys(stats).length === 0) {
    return <div className="ping-table-empty">No ping data available. Add IPs to monitor.</div>;
  }

  return (
    <div className="ping-table-container">
      <table className="ping-table">
        <thead>
          <tr>
            <th>Host</th>
            <th>Status</th>
            <th>Sent</th>
            <th>Received</th>
            <th>Loss</th>
            <th>Min RTT</th>
            <th>Avg RTT</th>
            <th>Max RTT</th>
            <th>Last Updated</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {Object.entries(stats).map(([ip, stat]) => (
            <Fragment key={ip}>
              <tr 
                className={stat.Running ? 'running' : 'stopped'}
                onClick={() => handleRowClick(ip)}
                style={{ cursor: 'pointer' }}
              >
                <td>{stat.Host}</td>
                <td>{stat.Running ? 'Running' : 'Stopped'}</td>
                <td>{stat.Sent}</td>
                <td>{stat.Received}</td>
                <td>{formatLoss(stat.Loss)}</td>
                <td>{formatTime(stat.MinRTT)}</td>
                <td>{formatTime(stat.AvgRTT)}</td>
                <td>{formatTime(stat.MaxRTT)}</td>
                <td>{formatDate(stat.LastUpdate)}</td>
                <td className="action-buttons">
                  <button 
                    className="reset-button" 
                    onClick={(e) => handleReset(ip, e)}
                    title="Reset statistics"
                  >
                    Reset
                  </button>
                  <button 
                    className="remove-button" 
                    onClick={(e) => handleRemove(ip, e)}
                    title="Remove this host"
                  >
                    Remove
                  </button>
                </td>
              </tr>
              {selectedHosts.includes(ip) && (
                <tr className="graph-row">
                  <td colSpan={10} className="graph-cell">
                    <PingGraph 
                      stats={stat} 
                      onClose={() => onSelectHost(ip)} 
                    />
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  );
};

export default PingTable;
