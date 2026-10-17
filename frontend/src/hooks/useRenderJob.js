import { useCallback, useEffect, useRef, useState } from 'react'
import { createRender, getRenderJob } from '../services/api'

const POLL_INTERVAL_MS = 1000

const IDLE = { job: null, error: '', submitting: false }

// El render es un trabajo en segundo plano: se encola, el servidor lo procesa
// con ffmpeg y el cliente solo sondea el estado hasta que termine. Este hook
// encapsula ese ciclo y se encarga de dejar de consultar cuando el modal se
// cierra, para no dejar timers colgando ni recargar el archivo cerrado.
export function useRenderJob(token) {
  const [state, setState] = useState(IDLE)
  const timerRef = useRef(null)
  const jobIdRef = useRef(null)

  const stopPolling = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current)
      timerRef.current = null
    }
  }, [])

  useEffect(() => stopPolling, [stopPolling])

  const reset = useCallback(() => {
    stopPolling()
    jobIdRef.current = null
    setState(IDLE)
  }, [stopPolling])

  const poll = useCallback(
    (id) => {
      stopPolling()
      timerRef.current = setTimeout(async () => {
        try {
          const job = await getRenderJob(id, token)
          setState({ job, error: '', submitting: false })

          if (job.status === 'queued' || job.status === 'running') {
            poll(id)
          }
        } catch (requestError) {
          stopPolling()
          setState({ job: null, error: requestError.message, submitting: false })
        }
      }, POLL_INTERVAL_MS)
    },
    [stopPolling, token],
  )

  const submit = useCallback(
    async (form) => {
      stopPolling()
      setState({ job: null, error: '', submitting: true })
      try {
        const job = await createRender(form, token)
        jobIdRef.current = job.id
        setState({ job, error: '', submitting: false })
        poll(job.id)
        return job.id
      } catch (requestError) {
        setState({ job: null, error: requestError.message, submitting: false })
        throw requestError
      }
    },
    [poll, stopPolling, token],
  )

  return { ...state, submit, reset }
}
