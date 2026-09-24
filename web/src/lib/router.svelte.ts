// A tiny history-based router: the app only has a handful of pages.

export const route = $state({ path: location.pathname })

addEventListener('popstate', () => {
  route.path = location.pathname
})

export function navigate(to: string, replace = false) {
  if (to === location.pathname) return
  if (replace) history.replaceState(null, '', to)
  else history.pushState(null, '', to)
  route.path = to
  scrollTo(0, 0)
}

/** onclick handler for internal <a href> links: plain clicks navigate in-app. */
export function link(e: MouseEvent) {
  const a = e.currentTarget as HTMLAnchorElement
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  e.preventDefault()
  navigate(a.pathname)
}
