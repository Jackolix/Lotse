// A tiny history-based router: the app only has a handful of pages.

export const route = $state({ path: location.pathname, search: location.search })

addEventListener('popstate', () => {
  route.path = location.pathname
  route.search = location.search
})

export function navigate(to: string, replace = false) {
  if (to === location.pathname + location.search) return
  if (replace) history.replaceState(null, '', to)
  else history.pushState(null, '', to)
  const url = new URL(to, location.href)
  route.path = url.pathname
  route.search = url.search
  scrollTo(0, 0)
}

/** A query parameter of the current page. */
export const param = (name: string) => new URLSearchParams(route.search).get(name)

/** onclick handler for internal <a href> links: plain clicks navigate in-app. */
export function link(e: MouseEvent) {
  const a = e.currentTarget as HTMLAnchorElement
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  e.preventDefault()
  navigate(a.pathname + a.search)
}
