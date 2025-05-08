import React, { useState } from 'react';

interface AddPingerProps {
  onAdd: (ips: string[]) => void;
}

const AddPinger: React.FC<AddPingerProps> = ({ onAdd }) => {
  const [ipInput, setIpInput] = useState<string>('');
  const [error, setError] = useState<string>('');

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setError('');

    // Split by comma, newline, or space and trim whitespace
    const ips = ipInput
      .split(/[\s,]+/)
      .map(ip => ip.trim())
      .filter(ip => ip !== '');

    if (ips.length === 0) {
      setError('Please enter at least one IP address or hostname');
      return;
    }

    // Basic IP validation (simple check, could be improved)
    const invalidIps = ips.filter(ip => {
      // Allow hostnames and IP addresses
      return !isValidIpOrHostname(ip);
    });

    if (invalidIps.length > 0) {
      setError(`Invalid IPs or hostnames: ${invalidIps.join(', ')}`);
      return;
    }

    onAdd(ips);
    setIpInput('');
  };

  // Basic validation for IP or hostname
  const isValidIpOrHostname = (input: string): boolean => {
    // Simple IP validation (IPv4)
    const ipv4Regex = /^(\d{1,3}\.){3}\d{1,3}$/;
    
    // Simple hostname validation
    const hostnameRegex = /^[a-zA-Z0-9]([a-zA-Z0-9\-\.]{0,61}[a-zA-Z0-9])?$/;
    
    return ipv4Regex.test(input) || hostnameRegex.test(input);
  };

  return (
    <div className="add-pinger-container">
      <form onSubmit={handleSubmit}>
        <div className="form-group">
          <label htmlFor="ip-input">Enter IP address(es) or hostname(s) to monitor:</label>
          <textarea
            id="ip-input"
            value={ipInput}
            onChange={e => setIpInput(e.target.value)}
            placeholder="Enter IPs or hostnames (separated by commas, spaces, or new lines)"
            rows={3}
            className="ip-input"
          />
        </div>
        {error && <div className="error-message">{error}</div>}
        <button type="submit" className="add-button">Add</button>
      </form>
    </div>
  );
};

export default AddPinger;
