export type DeviceStatus = 'active' | 'inactive'

export interface Device {
  device_id: string
  name: string
  location: string
  status: DeviceStatus
  created_at: string
  updated_at: string
}

export type AlertStatus = 'active' | 'acknowledged' | 'resolved'

// triggered_at is optional because the event published at creation time
// (internal/httpapi/telemetry.go) does not include it, while the full
// Alert returned by acknowledge/resolve does. Both shapes are handled.
export interface Alert {
  id: number
  device_id: string
  metric: string
  triggering_value: number
  status: AlertStatus
  triggered_at?: string
  acknowledged_at?: string | null
  resolved_at?: string | null
}

export interface TelemetryReading {
  event_id: string
  device_id: string
  metric: string
  value: number
  recorded_at: string
}

export interface RealtimeMessage {
  type: 'telemetry' | 'alert'
  data: TelemetryReading | Alert
}
