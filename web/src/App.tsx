import { useCallback, useEffect, useMemo, useState } from 'react'
import { acknowledgeAlert, listAlerts, listDevices, resolveAlert } from './api'
import { useRealtime } from './useRealtime'
import type { Alert, Device, RealtimeMessage, TelemetryReading } from './types'

const MAX_READINGS = 20

function isAlert(data: TelemetryReading | Alert): data is Alert {
  return 'status' in data && 'triggering_value' in data
}

export default function App() {
  const [devices, setDevices] = useState<Device[]>([])
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [readings, setReadings] = useState<TelemetryReading[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listDevices()
      .then((res) => setDevices(res.devices))
      .catch((err: Error) => setError(err.message))
    listAlerts()
      .then((res) => setAlerts(res.alerts))
      .catch((err: Error) => setError(err.message))
  }, [])

  const handleMessage = useCallback((msg: RealtimeMessage) => {
    if (msg.type === 'telemetry') {
      const reading = msg.data as TelemetryReading
      setReadings((prev) => [reading, ...prev].slice(0, MAX_READINGS))
      return
    }
    if (msg.type === 'alert' && isAlert(msg.data)) {
      const alert = msg.data
      setAlerts((prev) => [alert, ...prev.filter((a) => a.id !== alert.id)])
    }
  }, [])

  useRealtime(handleMessage)

  // "Active alerts" means unresolved: acknowledging a problem is not
  // fixing it, so an acknowledged alert still belongs on this list - the
  // same grouping the backend's own partial unique index uses.
  const unresolvedAlerts = useMemo(
    () => alerts.filter((a) => a.status === 'active' || a.status === 'acknowledged'),
    [alerts],
  )

  async function handleAcknowledge(id: number) {
    try {
      const updated = await acknowledgeAlert(id)
      setAlerts((prev) => prev.map((a) => (a.id === id ? updated : a)))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleResolve(id: number) {
    try {
      const updated = await resolveAlert(id)
      setAlerts((prev) => prev.map((a) => (a.id === id ? updated : a)))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <main>
      <h1>Edvexa IoT Dashboard</h1>
      {error && <p className="error">{error}</p>}

      <section>
        <h2>Devices</h2>
        <table>
          <thead>
            <tr>
              <th>Device ID</th>
              <th>Name</th>
              <th>Location</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {devices.map((d) => (
              <tr key={d.device_id}>
                <td>{d.device_id}</td>
                <td>{d.name}</td>
                <td>{d.location}</td>
                <td className={d.status}>{d.status}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section>
        <h2>Recent telemetry</h2>
        <p className="hint">
          Readings appear here as they are ingested; nothing is loaded retroactively on page load.
        </p>
        <table>
          <thead>
            <tr>
              <th>Device</th>
              <th>Metric</th>
              <th>Value</th>
              <th>Recorded at</th>
            </tr>
          </thead>
          <tbody>
            {readings.map((r) => (
              <tr key={r.event_id}>
                <td>{r.device_id}</td>
                <td>{r.metric}</td>
                <td>{r.value}</td>
                <td>{r.recorded_at}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section>
        <h2>Active alerts</h2>
        <table>
          <thead>
            <tr>
              <th>Device</th>
              <th>Metric</th>
              <th>Value</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {unresolvedAlerts.map((a) => (
              <tr key={a.id}>
                <td>{a.device_id}</td>
                <td>{a.metric}</td>
                <td>{a.triggering_value}</td>
                <td className={a.status}>{a.status}</td>
                <td>
                  {a.status === 'active' && (
                    <button onClick={() => handleAcknowledge(a.id)}>Acknowledge</button>
                  )}
                  <button onClick={() => handleResolve(a.id)}>Resolve</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </main>
  )
}
