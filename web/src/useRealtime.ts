import { useEffect, useRef } from 'react'
import type { RealtimeMessage } from './types'

// A fixed delay, not exponential backoff: this is a single local dashboard
// against a local API, not a fleet of clients that could overwhelm the
// server by retrying in lockstep.
const RECONNECT_DELAY_MS = 2000

// Subscribes to /v1/ws for the lifetime of the component and calls
// onMessage for every parsed frame. Reconnects automatically on close, so a
// server restart during development does not require a page reload.
export function useRealtime(onMessage: (msg: RealtimeMessage) => void) {
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage

  useEffect(() => {
    let socket: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let stopped = false

    function connect() {
      const url = `${location.origin.replace(/^http/, 'ws')}/v1/ws`
      socket = new WebSocket(url)

      socket.onmessage = (event: MessageEvent<string>) => {
        try {
          const msg = JSON.parse(event.data) as RealtimeMessage
          onMessageRef.current(msg)
        } catch {
          // A malformed frame is dropped rather than crashing the dashboard.
        }
      }

      socket.onclose = () => {
        if (!stopped) {
          reconnectTimer = setTimeout(connect, RECONNECT_DELAY_MS)
        }
      }
    }

    connect()

    return () => {
      stopped = true
      if (reconnectTimer) clearTimeout(reconnectTimer)
      socket?.close()
    }
  }, [])
}
