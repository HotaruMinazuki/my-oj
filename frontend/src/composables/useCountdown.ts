import { ref, computed, onMounted, onBeforeUnmount, type Ref } from 'vue'

/**
 * Live-updating countdown to a target time.
 *
 * Ticks once per second while the component is mounted; stops automatically
 * on unmount to prevent leaks.
 *
 * @param target  ISO 8601 timestamp (string) or Date.
 * @returns       `{ total, days, hours, minutes, seconds, expired, formatted }`
 */
export function useCountdown(target: Ref<string | Date | null | undefined>) {
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null

  onMounted(() => {
    timer = setInterval(() => { now.value = Date.now() }, 1000)
  })
  onBeforeUnmount(() => { if (timer) clearInterval(timer) })

  const total = computed(() => {
    if (!target.value) return 0
    const t = new Date(target.value).getTime()
    return Math.max(0, t - now.value)
  })

  const expired = computed(() => total.value <= 0)

  const days    = computed(() => Math.floor(total.value / 86_400_000))
  const hours   = computed(() => Math.floor((total.value % 86_400_000) / 3_600_000))
  const minutes = computed(() => Math.floor((total.value % 3_600_000) / 60_000))
  const seconds = computed(() => Math.floor((total.value % 60_000) / 1000))

  const formatted = computed(() => {
    if (expired.value) return '00:00:00'
    const pad = (n: number) => String(n).padStart(2, '0')
    if (days.value > 0) {
      return `${days.value}天 ${pad(hours.value)}:${pad(minutes.value)}:${pad(seconds.value)}`
    }
    return `${pad(hours.value)}:${pad(minutes.value)}:${pad(seconds.value)}`
  })

  return { total, days, hours, minutes, seconds, expired, formatted }
}
