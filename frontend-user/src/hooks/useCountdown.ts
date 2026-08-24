import { useEffect, useRef, useState } from 'react'

/**
 * useCountdown 把后端下发的 seconds_left 变成每秒自减的本地读数。
 *
 * 为什么不直接用 new Date(start_at) - Date.now()：客户端时钟可能不准
 * （用户手动改过时间、虚拟机休眠后未同步）。以服务端给的剩余秒数为基准、
 * 本地只做递减，能规避绝对时钟偏差；每次服务端推来新快照会重新校准。
 */
export function useCountdown(secondsLeft: number): number {
  const [left, setLeft] = useState(Math.max(0, secondsLeft))
  const anchorRef = useRef({ at: Date.now(), value: Math.max(0, secondsLeft) })

  // 服务端推来新值时重新锚定，而不是简单 setLeft ——
  // 否则页面在后台标签页被节流后，读数会与真实剩余时间越差越多。
  useEffect(() => {
    anchorRef.current = { at: Date.now(), value: Math.max(0, secondsLeft) }
    setLeft(Math.max(0, secondsLeft))
  }, [secondsLeft])

  useEffect(() => {
    if (anchorRef.current.value <= 0) return
    const tick = () => {
      const { at, value } = anchorRef.current
      const elapsed = Math.floor((Date.now() - at) / 1000)
      setLeft(Math.max(0, value - elapsed))
    }
    const id = setInterval(tick, 1000)
    // 标签页重新可见时立刻补算一次，避免用户看到一个明显停滞的旧读数。
    const onVisible = () => {
      if (document.visibilityState === 'visible') tick()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      clearInterval(id)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [secondsLeft])

  return left
}
