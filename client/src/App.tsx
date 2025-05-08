import { useState, useEffect } from 'react'
import './App.css'
import AddPinger from './components/AddPinger'
import PingTable from './components/PingTable'
// PingGraph is used directly in the table component
import api, { type PingStatsResponse } from './services/api'

function App() {
  const [stats, setStats] = useState<PingStatsResponse>({})
  const [loading, setLoading] = useState<boolean>(false)
  const [refreshInterval, setRefreshInterval] = useState<number>(5000) // 5 seconds by default
  const [intervalId, setIntervalId] = useState<number | null>(null)
  const [selectedHosts, setSelectedHosts] = useState<string[]>([])

  // Fetch ping stats
  const fetchStats = async () => {
    try {
      setLoading(true)
      const data = await api.getPingerStats()
      setStats(data)
    } catch (error) {
      console.error('Error fetching ping stats:', error)
    } finally {
      setLoading(false)
    }
  }

  // Handle adding new pingers
  const handleAddPinger = async (ips: string[]) => {
    try {
      setLoading(true)
      const result = await api.addPingers(ips)
      
      if (result.success) {
        // Refresh stats to see the new pingers
        await fetchStats()
      } else if (result.errors) {
        // Show errors if any occurred
        console.error('Errors adding pingers:', result.errors)
        alert('Some errors occurred while adding pingers: ' + 
              Object.entries(result.errors)
                .map(([ip, error]) => `${ip}: ${error}`)
                .join('\n'))
      }
    } catch (error) {
      console.error('Error adding pingers:', error)
      alert('Failed to add pingers. Please try again.')
    } finally {
      setLoading(false)
    }
  }

  // Handle resetting pingers
  const handleResetPinger = async (ips: string[]) => {
    try {
      setLoading(true)
      await api.resetPingers(ips)
      // Refresh stats after reset
      await fetchStats()
    } catch (error) {
      console.error('Error resetting pingers:', error)
      alert('Failed to reset pingers. Please try again.')
    } finally {
      setLoading(false)
    }
  }

  // Handle removing pingers
  const handleRemovePinger = async (ips: string[]) => {
    try {
      setLoading(true)
      await api.removePingers(ips)
      
      // Remove any selected hosts that are being removed
      const newSelectedHosts = selectedHosts.filter(host => !ips.includes(host))
      setSelectedHosts(newSelectedHosts)
      
      // Refresh stats after removal
      await fetchStats()
    } catch (error) {
      console.error('Error removing pingers:', error)
      alert('Failed to remove pingers. Please try again.')
    } finally {
      setLoading(false)
    }
  }
  
  // Handle selecting a host to show its graph
  const handleSelectHost = (ip: string) => {
    setSelectedHosts(prev => {
      // If already selected, remove it; otherwise add it
      return prev.includes(ip) 
        ? prev.filter(host => host !== ip)
        : [...prev, ip]
    })
  }

  // Setup auto-refresh
  useEffect(() => {
    // Clear existing interval if any
    if (intervalId !== null) {
      window.clearInterval(intervalId)
    }

    // Setup new interval if refresh interval is greater than 0
    if (refreshInterval > 0) {
      const id = window.setInterval(() => {
        fetchStats()
      }, refreshInterval)
      setIntervalId(Number(id))
    }

    // Initial fetch
    fetchStats()

    // Cleanup interval on component unmount
    return () => {
      if (intervalId !== null) {
        window.clearInterval(intervalId)
      }
    }
  }, [refreshInterval])

  // Handle refresh interval change
  const handleRefreshChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const value = parseInt(e.target.value, 10)
    setRefreshInterval(value)
  }

  return (
    <div className="app-container">
      <header className="app-header">
        <h1>PingMon</h1>
        <p>Monitor network connectivity to multiple hosts</p>
      </header>

      <AddPinger onAdd={handleAddPinger} />

      <div className="refresh-controls">
        <div className="refresh-rate-control">
          <label htmlFor="refresh-rate">Auto-refresh every:</label>
          <select 
            id="refresh-rate" 
            value={refreshInterval} 
            onChange={handleRefreshChange}
          >
            <option value="0">Off</option>
            <option value="1000">1 second</option>
            <option value="5000">5 seconds</option>
            <option value="10000">10 seconds</option>
            <option value="30000">30 seconds</option>
            <option value="60000">1 minute</option>
          </select>
        </div>
        <button 
          className="refresh-button" 
          onClick={() => fetchStats()}
          disabled={loading}
        >
          {loading ? 'Loading...' : 'Refresh Now'}
        </button>
      </div>

      {loading && Object.keys(stats).length === 0 ? (
        <div className="loading">Loading ping data...</div>
      ) : (
        <PingTable 
          stats={stats} 
          onReset={handleResetPinger} 
          onRemove={handleRemovePinger}
          onSelectHost={handleSelectHost}
          selectedHosts={selectedHosts}
        />
      )}
    </div>
  )
}

export default App
