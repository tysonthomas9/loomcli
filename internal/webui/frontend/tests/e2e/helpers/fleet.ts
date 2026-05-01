import type { Page } from "@playwright/test"

export const WORKSPACE_ID = "default"
export const WS_API = `/api/workspaces/${WORKSPACE_ID}`

export function workspaceApi(workspaceId = WORKSPACE_ID) {
  return `/api/workspaces/${workspaceId}`
}

export function ok<T>(data: T) {
  return { success: true, data }
}

export function workspacePath(path = "/") {
  if (path.startsWith(`/ws/${WORKSPACE_ID}/`)) return path
  if (path === "/" || path === "") return `/ws/${WORKSPACE_ID}/kanban`
  if (path.startsWith("/?")) {
    const params = new URLSearchParams(path.slice(2))
    const view = params.get("view") ?? "kanban"
    params.delete("view")
    const search = params.toString()
    return `/ws/${WORKSPACE_ID}/${view}${search ? `?${search}` : ""}`
  }
  return path
}

export function workspaceData(workspaceId = WORKSPACE_ID, name = "Default") {
  return {
    id: workspaceId,
    name,
    path: `/tmp/${workspaceId}`,
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      {
        id: workspaceId,
        name,
        path: `/tmp/${workspaceId}`,
        active: true,
        repo_count: 0,
        is_default: workspaceId === WORKSPACE_ID,
      },
    ],
    workspace_order: [workspaceId],
    default_workspace: name,
  }
}

export async function setupFleetMocks(
  page: Page,
  issues: object[],
  patchCalls?: { url: string; body: object }[],
  options?: {
    issuesDelayMs?: number
    blockedIssues?: object[]
    workspaceId?: string
  },
) {
  const workspaceId = options?.workspaceId ?? WORKSPACE_ID
  const api = workspaceApi(workspaceId)
  const blockedIssues = options?.blockedIssues ?? []
  const servedIssues =
    blockedIssues.length === 0
      ? issues
      : issues.map((issue) => {
          const issueId = (issue as { id?: string }).id
          const blockedIssue = blockedIssues.find(
            (blocked) => (blocked as { id?: string }).id === issueId,
          )
          if (!blockedIssue) return issue
          return { ...issue, ...blockedIssue, is_blocked: true }
        })

  await page.route("**/*", async (route) => {
    const request = route.request()
    const pathname = new URL(request.url()).pathname

    if (pathname === "/api/config") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ mode: "open" }),
      })
    } else if (pathname === "/api/workspaces/active" || pathname === api) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(workspaceData(workspaceId))),
      })
    } else if (
      pathname === `${api}/issues` ||
      pathname === `${api}/ready` ||
      pathname === `${api}/issues/graph`
    ) {
      if (options?.issuesDelayMs) {
        await new Promise((resolve) =>
          setTimeout(resolve, options.issuesDelayMs),
        )
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(servedIssues)),
      })
    } else if (pathname === `${api}/stats`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_issues: servedIssues.length,
          open_issues: servedIssues.filter(
            (i) => (i as { status?: string }).status === "open",
          ).length,
          in_progress_issues: servedIssues.filter(
            (i) => (i as { status?: string }).status === "in_progress",
          ).length,
          closed_issues: servedIssues.filter(
            (i) => (i as { status?: string }).status === "closed",
          ).length,
          blocked_issues: blockedIssues.length,
          deferred_issues: 0,
          ready_issues: servedIssues.filter(
            (i) => (i as { status?: string }).status === "open",
          ).length,
          tombstone_issues: 0,
          pinned_issues: 0,
          epics_eligible_for_closure: 0,
          average_lead_time_hours: 0,
        }),
      })
    } else if (pathname === `${api}/blocked`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(blockedIssues)),
      })
    } else if (pathname === `${api}/terminal/tabs`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([])),
      })
    } else if (pathname === `${api}/terminal/state`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      })
    } else if (
      request.method() === "PATCH" &&
      pathname.startsWith(`${api}/issues/`)
    ) {
      const issueId = pathname.split("/").pop()
      const body = request.postDataJSON() as object
      patchCalls?.push({ url: request.url(), body })
      const issue = servedIssues.find(
        (i) => (i as { id: string }).id === issueId,
      )

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok({ ...(issue ?? {}), ...body })),
      })
    } else if (
      request.method() === "GET" &&
      pathname.startsWith(`${api}/issues/`)
    ) {
      const issueId = pathname.split("/").pop()
      const issue = servedIssues.find(
        (i) => (i as { id: string }).id === issueId,
      )

      await route.fulfill({
        status: issue ? 200 : 404,
        contentType: "application/json",
        body: JSON.stringify(
          issue ? ok(issue) : { success: false, error: "Issue not found" },
        ),
      })
    } else if (pathname.startsWith("/api/") && pathname.includes("/events")) {
      await route.abort()
    } else {
      await route.continue()
    }
  })
}

export async function waitForWorkspaceIssues(page: Page) {
  return page.waitForResponse(
    (res) =>
      new URL(res.url()).pathname === `${WS_API}/issues` &&
      res.status() === 200,
  )
}

export async function showMoreFilters(page: Page) {
  if ((await page.getByLabel("Group issues by").count()) > 0) return

  const moreFilters = page.getByRole("button", { name: "More filters" })
  if (
    (await moreFilters.count()) > 0 &&
    (await moreFilters.first().isVisible())
  ) {
    await moreFilters.first().click({ force: true })
  }
}
