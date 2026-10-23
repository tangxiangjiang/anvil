window.api = {
  tokenKey: "anvil_web_token",
  getToken() { return localStorage.getItem(this.tokenKey) || ""; },
  setToken(t) {
    const changed = (localStorage.getItem(this.tokenKey) || "") !== (t || "");
    if (t) localStorage.setItem(this.tokenKey, t);
    else localStorage.removeItem(this.tokenKey);
    // Volatile snapshots are user-scoped. Never let a logout/login transition
    // expose one authenticated user's last successful view to another.
    if (changed) window.AnvilUI?.swr?.clearAll();
  },
  async req(method, path, body) {
    const headers = { Accept: "application/json" };
    const tok = this.getToken();
    if (tok) headers.Authorization = "Bearer " + tok;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = { raw: text }; }
    if (!res.ok) {
      const err = new Error((data && (data.error || data.message || data.Message)) || text || res.statusText);
      err.status = res.status;
      err.data = data;
      err.requestId = res.headers.get("X-Request-ID") || "";
      throw err;
    }
    return data;
  },
  v1: {
    setupStatus() { return api.req("GET", "/api/v1/setup/status"); },
    setupAdmin(username, password) {
      return api.req("POST", "/api/v1/setup/admin", { username, password });
    },
    login(username, password) {
      return api.req("POST", "/api/v1/auth/login", { username, password });
    },
    logout() { return api.req("POST", "/api/v1/auth/logout", {}); },
    me() { return api.req("GET", "/api/v1/auth/me"); },
    nodes() { return api.req("GET", "/api/v1/nodes"); },
    patchNode(id, body) { return api.req("PATCH", "/api/v1/nodes/" + encodeURIComponent(id), body); },
    clusters() { return api.req("GET", "/api/v1/clusters"); },
    patchClusterNode(fleetId, nodeId, body) {
      return api.req(
        "PATCH",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" + encodeURIComponent(nodeId),
        body,
      );
    },
    addClusterWorker(fleetId, nodeId, body) {
      return api.req(
        "POST",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" + encodeURIComponent(nodeId) + "/workers",
        body,
      );
    },
    removeClusterWorker(fleetId, nodeId, workerId) {
      return api.req(
        "DELETE",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" + encodeURIComponent(nodeId) +
          "/workers/" + encodeURIComponent(workerId),
      );
    },
    startClusterWorker(fleetId, nodeId, workerId) {
      return api.req(
        "POST",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" + encodeURIComponent(nodeId) +
          "/workers/" + encodeURIComponent(workerId) + "/start",
        {},
      );
    },
    shutdownClusterWorker(fleetId, nodeId, workerId) {
      return api.req(
        "POST",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" + encodeURIComponent(nodeId) +
          "/workers/" + encodeURIComponent(workerId) + "/shutdown",
        {},
      );
    },
    listSandbox(fleetId, nodeId, path) {
      let u = "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" +
        encodeURIComponent(nodeId) + "/sandbox";
      if (path) u += "?path=" + encodeURIComponent(path);
      return api.req("GET", u);
    },
    reviewTree(fleetId, nodeId, workerId, path) {
      const q = new URLSearchParams({ worker: workerId });
      if (path) q.set("path", path);
      return api.req(
        "GET",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" +
          encodeURIComponent(nodeId) + "/sandbox?" + q.toString(),
      );
    },
    reviewFile(fleetId, nodeId, workerId, path) {
      const q = new URLSearchParams({ worker: workerId, path });
      return api.req(
        "GET",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" +
          encodeURIComponent(nodeId) + "/sandbox/file?" + q.toString(),
      );
    },
    reviewStatus(fleetId, nodeId, workerId) {
      const q = new URLSearchParams({ worker: workerId });
      return api.req(
        "GET",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" +
          encodeURIComponent(nodeId) + "/git/status?" + q.toString(),
      );
    },
    reviewDiff(fleetId, nodeId, workerId, path, staged) {
      const q = new URLSearchParams({ worker: workerId });
      if (path) q.set("path", path);
      if (staged) q.set("staged", "true");
      return api.req(
        "GET",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" +
          encodeURIComponent(nodeId) + "/git/diff?" + q.toString(),
      );
    },
    mkdirSandbox(fleetId, nodeId, path) {
      return api.req(
        "POST",
        "/api/v1/clusters/" + encodeURIComponent(fleetId) + "/nodes/" +
          encodeURIComponent(nodeId) + "/sandbox/mkdir",
        { path },
      );
    },
    listWorkspaces(fleetId) {
      return api.req("GET", "/api/v1/workspaces?fleet_id=" + encodeURIComponent(fleetId));
    },
    createWorkspace(body) {
      return api.req("POST", "/api/v1/workspaces", body);
    },
    deleteWorkspace(id) {
      return api.req("DELETE", "/api/v1/workspaces/" + encodeURIComponent(id));
    },
    workspaceGraph(id) {
      return api.req("GET", "/api/v1/workspaces/" + encodeURIComponent(id) + "/graph");
    },
    listProjects(wsId) {
      return api.req("GET", "/api/v1/workspaces/" + encodeURIComponent(wsId) + "/projects");
    },
    createProject(wsId, body) {
      return api.req("POST", "/api/v1/workspaces/" + encodeURIComponent(wsId) + "/projects", body);
    },
    deleteProject(wsId, projId) {
      return api.req(
        "DELETE",
        "/api/v1/workspaces/" + encodeURIComponent(wsId) + "/projects/" + encodeURIComponent(projId),
      );
    },
    patchProject(wsId, projId, body) {
      return api.req(
        "PATCH",
        "/api/v1/workspaces/" + encodeURIComponent(wsId) + "/projects/" + encodeURIComponent(projId),
        body,
      );
    },
    getProject(wsId, projId) {
      return api.req(
        "GET",
        "/api/v1/workspaces/" + encodeURIComponent(wsId) + "/projects/" + encodeURIComponent(projId),
      );
    },
    bindWorker(wsId, projId, body) {
      return api.req(
        "POST",
        "/api/v1/workspaces/" + encodeURIComponent(wsId) + "/projects/" +
          encodeURIComponent(projId) + "/bind-worker",
        body,
      );
    },
    unbindWorker(wsId, projId, workerId) {
      return api.req(
        "POST",
        "/api/v1/workspaces/" + encodeURIComponent(wsId) + "/projects/" +
          encodeURIComponent(projId) + "/unbind-worker",
        { worker_id: workerId },
      );
    },
    usage() { return api.req("GET", "/api/v1/usage"); },
    search(q) { return api.req("GET", "/api/v1/search?q=" + encodeURIComponent(q)); },
    health() { return api.req("GET", "/api/v1/health"); },
    firstRun() { return api.req("GET", "/api/v1/diagnostics/first-run"); },
    classifyError(body) { return api.req("POST", "/api/v1/diagnostics/classify-error", body); },
    jobs(suffix) { return api.req("GET", "/api/v1/jobs" + (suffix || "")); },
    jobEvents(id) { return api.req("GET", "/api/v1/jobs/" + encodeURIComponent(id) + "/events"); },
    jobCancel(id) { return api.req("POST", "/api/v1/jobs/" + encodeURIComponent(id) + "/cancel", {}); },
    deliveryAssist(body) { return api.req("POST", "/api/v1/delivery/assist", body); },
    deliveryAudit(kind, id, limit) {
      const q = new URLSearchParams({ kind, id, limit: String(limit || 50) }).toString();
      return api.req("GET", "/api/v1/delivery/audit?" + q);
    },
    deliveryPreflight(transition, target) {
      const q = new URLSearchParams({ transition, target }).toString();
      return api.req("GET", "/api/v1/delivery/preflight?" + q);
    },
    deliveryReadiness(scope) {
      const q = new URLSearchParams();
      for (const [key, value] of Object.entries(scope || {})) {
        if (value !== "") q.set(key, value);
      }
      return api.req("GET", "/api/v1/delivery/readiness?" + q.toString());
    },
    delivery() { return api.req("GET", "/api/v1/delivery"); },

    // Workbench (0.46.7): the console's interactive session surface.
    workbenchSessions() { return api.req("GET", "/api/v1/workbench/sessions"); },
    workbenchStart(body) { return api.req("POST", "/api/v1/workbench/sessions", body); },
    workbenchSession(id) {
      return api.req("GET", "/api/v1/workbench/sessions/" + encodeURIComponent(id));
    },
    workbenchCancel(id) {
      return api.req("POST", "/api/v1/workbench/sessions/" + encodeURIComponent(id) + "/cancel", {});
    },
    workbenchApproval(id, requestId, body) {
      return api.req(
        "POST",
        "/api/v1/workbench/sessions/" + encodeURIComponent(id) + "/approvals/" + encodeURIComponent(requestId),
        body,
      );
    },
    workbenchEventsURL(id) {
      return "/api/v1/workbench/sessions/" + encodeURIComponent(id) + "/events";
    },
    deliveryCreateEng(body) { return api.req("POST", "/api/v1/delivery/eng-reqs", body); },
    deliveryEnsureProject(body) {
      return api.req("POST", "/api/v1/delivery/eng-reqs/ensure-project", body);
    },
    deliveryNextMilestone(params) {
      const q = new URLSearchParams(params || {}).toString();
      return api.req("GET", "/api/v1/delivery/next-milestone" + (q ? "?" + q : ""));
    },
    deliveryAddMajor(body) { return api.req("POST", "/api/v1/delivery/majors", body); },
    deliveryPatchMajor(major, body) {
      return api.req("PATCH", "/api/v1/delivery/majors/" + encodeURIComponent(major), body);
    },
    deliveryAdvanceMajor(major) {
      return api.req("POST", "/api/v1/delivery/majors/" + encodeURIComponent(major) + "/advance", {});
    },
    deliveryDeleteMajor(major) {
      return api.req("DELETE", "/api/v1/delivery/majors/" + encodeURIComponent(major));
    },
    deliveryPreviewDeleteMajor(major) {
      return api.req("GET", "/api/v1/delivery/majors/" + encodeURIComponent(major) + "/preview-delete");
    },
    deliveryTrash() { return api.req("GET", "/api/v1/delivery/trash"); },
    deliveryRestoreTrash(id, mode) {
      return api.req("POST", "/api/v1/delivery/trash/" + encodeURIComponent(id) + "/restore", { mode: mode || "" });
    },
    deliveryPurgeTrash(id) {
      return api.req("DELETE", "/api/v1/delivery/trash/" + encodeURIComponent(id));
    },
    deliveryAddVersion(engId, body) {
      return api.req("POST", "/api/v1/delivery/eng-reqs/" + encodeURIComponent(engId) + "/versions", body);
    },
    deliveryDeleteMilestone(engId, major, minor) {
      return api.req(
        "DELETE",
        "/api/v1/delivery/eng-reqs/" + encodeURIComponent(engId) +
          "/milestones/" + major + "/" + minor,
      );
    },
    deliverySetMilestoneStatus(engId, major, minor, status) {
      return api.req(
        "PATCH",
        "/api/v1/delivery/eng-reqs/" + encodeURIComponent(engId) +
          "/milestones/" + major + "/" + minor,
        { status },
      );
    },
    deliveryPatchVersion(engId, major, minor, patch, body) {
      return api.req(
        "PATCH",
        "/api/v1/delivery/eng-reqs/" + encodeURIComponent(engId) +
          "/versions/" + major + "/" + minor + "/" + patch,
        body,
      );
    },
    deliveryDeleteVersion(engId, major, minor, patch) {
      return api.req(
        "DELETE",
        "/api/v1/delivery/eng-reqs/" + encodeURIComponent(engId) +
          "/versions/" + major + "/" + minor + "/" + patch,
      );
    },
    deliveryApproveVersion(engId, major, minor, patch) {
      return api.req(
        "POST",
        "/api/v1/delivery/eng-reqs/" + encodeURIComponent(engId) +
          "/versions/" + major + "/" + minor + "/" + patch + "/approve",
        {},
      );
    },
    deliveryCreateDesign(body) {
      return api.req("POST", "/api/v1/delivery/designs", body);
    },
    deliveryPatchDesign(id, body) {
      return api.req("PATCH", "/api/v1/delivery/designs/" + encodeURIComponent(id), body);
    },
    deliveryApproveDesign(id) {
      return api.req("POST", "/api/v1/delivery/designs/" + encodeURIComponent(id) + "/approve", {});
    },
    deliveryBootstrapDesign(id) {
      return api.req("POST", "/api/v1/delivery/designs/" + encodeURIComponent(id) + "/bootstrap", {});
    },
    deliveryDeleteDesign(id) {
      return api.req("DELETE", "/api/v1/delivery/designs/" + encodeURIComponent(id));
    },
    deliveryAddDesignVersion(designId, body) {
      return api.req(
        "POST",
        "/api/v1/delivery/designs/" + encodeURIComponent(designId) + "/versions",
        body || {},
      );
    },
    deliveryPatchDesignVersion(designId, versionId, body) {
      return api.req(
        "PATCH",
        "/api/v1/delivery/designs/" +
          encodeURIComponent(designId) +
          "/versions/" +
          encodeURIComponent(versionId),
        body,
      );
    },
    deliveryToggleGateCheck(designId, versionId, body) {
      return api.req(
        "POST",
        "/api/v1/delivery/designs/" +
          encodeURIComponent(designId) +
          "/versions/" +
          encodeURIComponent(versionId) +
          "/gate-check",
        body || {},
      );
    },
    deliveryDeleteDesignVersion(designId, versionId) {
      return api.req(
        "DELETE",
        "/api/v1/delivery/designs/" +
          encodeURIComponent(designId) +
          "/versions/" +
          encodeURIComponent(versionId),
      );
    },
    deliveryPatchMilestonePos(id, x, y) {
      return api.req(
        "PATCH",
        "/api/v1/delivery/roadmap/milestones/" + encodeURIComponent(id),
        { x, y },
      );
    },
    deliveryPatchMilestone(id, body) {
      return api.req(
        "PATCH",
        "/api/v1/delivery/roadmap/milestones/" + encodeURIComponent(id),
        body || {},
      );
    },
    deliveryToggleAcceptanceCheck(msId, body) {
      return api.req(
        "POST",
        "/api/v1/delivery/roadmap/milestones/" +
          encodeURIComponent(msId) +
          "/acceptance-check",
        body || {},
      );
    },
    deliveryMergeMilestone(msId, body) {
      return api.req(
        "POST",
        "/api/v1/delivery/roadmap/milestones/" +
          encodeURIComponent(msId) +
          "/merge",
        body || {},
      );
    },
    deliveryCreatePolish(body) {
      return api.req("POST", "/api/v1/delivery/polishes", body || {});
    },
    deliveryRunPolish(id, body) {
      return api.req(
        "POST",
        "/api/v1/delivery/polishes/" + encodeURIComponent(id) + "/run",
        body || {},
      );
    },
    deliveryCreateBuglist(body) {
      return api.req("POST", "/api/v1/delivery/buglist", body || {});
    },
    deliveryCreateTestCases(body) {
      return api.req("POST", "/api/v1/delivery/test-cases", body || {});
    },
    deliveryRunTestCases(body) {
      return api.req("POST", "/api/v1/delivery/test-cases/run", body || {});
    },
    deliveryFixBuglist(id, body) {
      return api.req(
        "POST",
        "/api/v1/delivery/buglist/" + encodeURIComponent(id) + "/fix",
        body || {},
      );
    },
    deliveryRegressBuglist(body) {
      return api.req("POST", "/api/v1/delivery/buglist/regress", body || {});
    },
    deliveryCreateJob(body) {
      return api.req("POST", "/api/v1/delivery/jobs", body || {});
    },
    /** Absolute URL for EventSource (same origin + session cookie). */
    deliveryJobEventsURL(jobId) {
      return "/api/v1/delivery/jobs/" + encodeURIComponent(jobId) + "/events";
    },
  },
};
