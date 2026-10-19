"use strict";

(() => {
  const $ = (id) => document.getElementById(id);
  const pages = ["overview", "tasks", "new", "storages", "files", "routes", "preferences", "diagnostics"];
  const state = { authenticated: false, csrf: "", language: "zh-Hans", words: {}, storages: [], users: [], capabilities: [], metadata: false, page: "overview", sequence: 0, abort: null, loading: false, submitting: false, taskOffset: 0, fileOffset: 0, hasActiveTasks: true };
  let searchTimer, toastTimer, refreshTimer;
  const taskActions = new Map();
  const t = (key) => state.words[key] || key;
  const node = (tag, text, className) => {
    const element = document.createElement(tag);
    if (text !== undefined && text !== null) element.textContent = String(text);
    if (className) element.className = className;
    return element;
  };
  const date = (value) => value ? new Date(value).toLocaleString(state.language) : "—";
  const bytes = (value) => {
    const size = Number(value || 0);
    if (size < 1024) return `${size} B`;
    const units = ["KB", "MB", "GB", "TB"];
    const exponent = Math.min(Math.floor(Math.log(size) / Math.log(1024)), 4);
    return `${(size / 1024 ** exponent).toFixed(1)} ${units[exponent - 1]}`;
  };
  function duration(seconds) {
    const hours = Math.floor(seconds / 3600);
    const minutes = Math.floor(seconds % 3600 / 60);
    return hours ? `${hours} ${t("hours")} ${minutes} ${t("minutes")}` : `${minutes} ${t("minutes")}`;
  }
  function toast(message) {
    clearTimeout(toastTimer);
    $("toast").textContent = message;
    $("toast").hidden = false;
    toastTimer = setTimeout(() => { $("toast").hidden = true; }, 4500);
  }
  function showLogin(message = "") {
    management.logout();
    state.authenticated = false;
    state.csrf = "";
    state.metadata = false;
    state.storages = [];
    state.users = [];
    state.capabilities = [];
    state.hasActiveTasks = true;
    clearTimeout(refreshTimer);
    state.sequence++;
    if (state.abort) state.abort.abort();
    $("console").hidden = true;
    $("login-view").hidden = false;
    $("boot").hidden = true;
    $("password").value = "";
    $("login-error").textContent = message;
    $("login-error").hidden = !message;
    $("task-dialog").close();
    $("input-dialog").close();
    preferences.close();
    $("password").focus();
  }
  async function request(path, options = {}) {
    const headers = new Headers(options.headers);
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (state.csrf) headers.set("X-CSRF-Token", state.csrf);
    const response = await fetch(`/admin/api/v1/${path}`, { ...options, headers, credentials: "same-origin", cache: "no-store" });
    const data = await response.json();
    if (!response.ok) {
      if (response.status === 401 && path !== "login") showLogin(t("sessionExpired"));
      const error = new Error(t(data.error || "internal_error"));
      error.selection = data;
      error.status = response.status;
      throw error;
    }
    return data;
  }
  async function loadLanguage(value) {
    state.language = value === "en" ? "en" : "zh-Hans";
    const response = await fetch(`/admin/assets/locales/${state.language}.json`, { cache: "no-store" });
    if (!response.ok) throw new Error("Unable to load language resources");
    const words = await response.json();
    state.words = words;
    document.documentElement.lang = state.language;
    document.title = `SaveAny · ${t("consoleName")}`;
    document.querySelectorAll("[data-i18n]").forEach((element) => { element.textContent = t(element.dataset.i18n); });
    document.querySelectorAll("[data-i18n-placeholder]").forEach((element) => { element.placeholder = t(element.dataset.i18nPlaceholder); });
    document.querySelectorAll("[data-i18n-aria-label]").forEach((element) => { element.setAttribute("aria-label", t(element.dataset.i18nAriaLabel)); });
  }
  function definitions(target, values) {
    target.replaceChildren();
    for (const [label, value] of values) {
      const dd=node("dd"); dd.append(value instanceof Node ? value : node("span",typeof value === "boolean" ? t(value ? "yes" : "no") : (value ?? "—")));
      target.append(node("dt",label),dd);
    }
  }
  function badge(status) { return node("span", t(status), `badge ${status}`); }
  function button(label, action, className = "secondary small-button") {
    const result = node("button", label, className);
    result.type = "button";
    result.addEventListener("click", action);
    return result;
  }
  function table(target, labels, rows) {
    target.replaceChildren();
    if (!rows.length) { target.append(node("p", t("empty"), "empty")); return; }
    const wrap = node("div", null, "table-wrap");
    const result = node("table");
    const header = node("thead"), headerRow = node("tr"), body = node("tbody");
    labels.forEach((label) => headerRow.append(node("th", t(label))));
    header.append(headerRow);
    rows.forEach((cells) => {
      const row = node("tr");
      cells.forEach((value) => {
        const cell = node("td");
        cell.append(value instanceof Node ? value : node("span", value ?? "—"));
        row.append(cell);
      });
      body.append(row);
    });
    result.append(header, body); wrap.append(result); target.append(wrap);
  }
  function pagination(target, total, offset, action) {
    target.replaceChildren(node("span", `${total ? offset + 1 : 0}–${Math.min(offset + 100, total)} / ${total}`));
    const previous = button(t("previousPage"), () => action(Math.max(0, offset - 100)));
    previous.disabled = offset === 0;
    const next = button(t("nextPage"), () => action(offset + 100));
    next.disabled = offset + 100 >= total;
    target.append(previous, next);
  }
  function scopeLabel(task) { return t(task.scope === "upload" ? "scope_upload" : task.scope === "tracked" ? "trackedScope" : task.scope === "history" ? "historyScope" : "queueScope"); }
  function inputLines(report) {
    const lines = (report.input_results || []).map((result) =>
      '#' + result.index + ' · ' + t('input_status_' + result.status) + ' · ' + t('input_code_' + result.code) + ' · ' + t("inputNewFiles") + ': ' + result.new_files +
      (result.album_fallback === true ? ' · ' + t("inputAlbumFallback") : ''));
    if (report.input_results_omitted) lines.push(t("inputOmitted") + ': ' + report.input_results_omitted);
    return lines;
  }
  function selectionDetails(report, heading) {
    const lines = inputLines(report);
    if (!lines.length) return;
    $("input-heading").textContent = heading;
    $("input-details").textContent = t("inputSelectionNote") + "\n\n" + lines.join("\n");
    if (!$("input-dialog").open) $("input-dialog").showModal();
  }
  function copyProgressLines(task) {
    const p = task.copy_progress, counts = task.result_summary;
    if (!p) return [];
    const terminal = ["completed", "failed", "cancelled"].includes(task.status);
    const lines = [];
    if (!terminal) {
      if (p.stage === "scanning") lines.push(`${t("copyScanning")}: ${p.matched} / ${p.requested}`);
      else lines.push(`${t("copyForwarding")}: ${p.processed} / ${p.matched} (${Math.floor(100 * p.processed / Math.max(1, p.matched))}%)`);
    }
    if (terminal) {
      if (task.status === "completed" && p.matched === 0) lines.push(t("copyNoMatches"));
      lines.push(`${t("copyRequested")}: ${p.requested} · ${t("copyMatched")}: ${p.matched}`);
    }
    if (counts && counts.total > 0 && (terminal || p.stage === "forwarding")) {
      lines.push(`${t("resultSucceeded")}: ${counts.succeeded} · ${t("resultFailed")}: ${counts.failed}`);
      if (terminal && counts.pending + counts.cancelled + counts.interrupted > 0) lines.push(`${t("copyUnfinished")}: ${counts.pending + counts.cancelled + counts.interrupted}`);
    }
    lines.push(t("copyCountUnit"));
    return lines;
  }
  function taskDetails(task) {
    const values = [[t("taskID"), task.task_id], [t("title"), task.title], [t("status"), t(taskStatusKey(task))],
      [t("scope"), scopeLabel(task)], [t("taskType"), t(task.type || "unknownType")],
      [t("source"), t(`source_${task.source || "unknown"}`)], [t("storage"), task.storage], [t("path"), task.path], [t("createdAt"), date(task.created_at)]];
    if (task.delivery_progress) {
      values.push([t("tgCollection"),["queued","running","cancelling"].includes(task.status) ? t(`collection_${task.delivery_progress.stage}`) : t(task.status)]);
      if (task.delivery_progress.total_bytes && ["running","cancelling"].includes(task.status)) values.push([t("progress"),`${bytes(task.delivery_progress.bytes)} / ${bytes(task.delivery_progress.total_bytes)}`]);
    }
    for (const link of task.result_links || []) {
      if (!/^https:\/\/t\.me\//.test(link)) continue;
      const anchor=node("a",t("openCollection")); anchor.href=link;anchor.target="_blank";anchor.rel="noopener noreferrer";
      values.push([t("tgCollection"),anchor]);
    }
    if (task.copy_progress) values.push([t("progressResult"), copyProgressLines(task).join("\n")]);
    if (task.progress) {
      values.push([t(task.downloading ? "downloadProgress" : "progress"), `${Number(task.progress.percent || 0).toFixed(1)}%`],
        [t("downloaded"), `${bytes(task.progress.downloaded_bytes)} / ${bytes(task.progress.total_bytes)}`]);
      if (task.progress.total_files) values.push([t("fileCount"), `${task.progress.downloaded_files || 0} / ${task.progress.total_files}`]);
    }
    for (const line of inputLines(task)) values.push([t("inputResults"), line]);
    if (task.result_summary && !task.downloading) {
      const counts = task.result_summary;
      if (task.type === "copy") {
        values.push([t("resultUnit"), t("copyResultUnit")],
          [t("selectedItems"), counts.total], [t("succeededItems"), counts.succeeded],
          [t("failedItems"), counts.failed], [t("cancelledItems"), counts.cancelled],
          [t("interruptedItems"), counts.interrupted], [t("unattemptedItems"), counts.pending],
          [t("runningItems"), counts.running], [t("resultMeaning"), t("copyResultMeaning")]);
      } else values.push([t("fileCount"), counts.total], [t("succeededItems"), counts.succeeded],
        [t("unsavedItems"), counts.failed], [t("cancelledItems"), counts.cancelled],
        [t("interruptedItems"), counts.interrupted], [t("unattemptedItems"), counts.pending],
        [t("runningItems"), counts.running], [t("resultMeaning"), t("fileResultMeaning")]);
    }
    if (task.copy_outcome) values.push([t("result"), t(`copyOutcome_${task.copy_outcome}`)]);
    for (const file of task.upload_files || []) {
      const detail = task.downloading && file.status === "preparing"
        ? `${t("upload_preparing")} · ${bytes(file.size)}`
        : `${t(`upload_${file.status}`)} · ${bytes(file.uploaded_bytes)} / ${bytes(file.size)}`;
      values.push([file.saved_path || file.path, detail]);
      if (file.next_attempt_at && file.status === "retry_wait") values.push([t("nextRetry"), date(file.next_attempt_at)]);
      if (file.error) values.push([t("error"), file.error]);
      if (file.cleanup_error) values.push([t("cleanupPending"), file.cleanup_error]);
    }
    definitions($("task-details"), values);
    $("task-dialog").dataset.taskId = task.task_id;
    $("task-detail-error").textContent = task.error || "";
    $("task-detail-error").hidden = !task.error;
    if (!$("task-dialog").open) $("task-dialog").showModal();
  }
  async function taskAction(task, trigger) {
    if (taskActions.has(task.task_id)) return;
    const deleting = task.can_delete;
    taskActions.set(task.task_id, deleting ? "deletingTask" : "cancelling");
    trigger.disabled = true;
    trigger.textContent = t(taskActions.get(task.task_id));
    try {
      const result = await request(`tasks/${encodeURIComponent(task.task_id)}${deleting ? "" : "/cancel"}`, { method: deleting ? "DELETE" : "POST" });
      toast(t(deleting ? "deletedTask" : result.status));
      if (deleting) document.querySelectorAll("[data-task-action]").forEach((action) => {
        if (action.dataset.taskAction === task.task_id) action.closest("tr")?.remove();
      });
      if (deleting && $("task-dialog").dataset.taskId === task.task_id) $("task-dialog").close();
    } catch (error) { toast(error.message); }
    finally {
      await loadPage();
      taskActions.delete(task.task_id);
      document.querySelectorAll("[data-task-action]").forEach((action) => {
        if (action.dataset.taskAction !== task.task_id) return;
        action.disabled = action.dataset.available !== "true";
        action.textContent = t(action.dataset.label);
      });
    }
  }
  function tasksTable(target, tasks) {
    table(target, ["task", "status", "progressResult", "storage", "createdAt", "actions"], tasks.map((task) => {
      const title = node("div");
      title.append(node("div", task.title || task.task_id, "cell-title"), node("div", `${task.task_id} · ${t(`source_${task.source || "unknown"}`)} · ${scopeLabel(task)}`, "cell-subtitle"));
      const progress = node("div", null, "progress");
      if (task.delivery_progress) {
        const active=["queued","running","cancelling"].includes(task.status), p=task.delivery_progress;
        progress.append(node("span",active ? t(`collection_${p.stage}`) : t(task.status)));
        if(active && p.total_bytes>0){const meter=node("progress");meter.max=p.total_bytes;meter.value=Math.min(p.total_bytes,p.bytes||0);meter.setAttribute("aria-label",t(`collection_${p.stage}`));progress.append(meter,node("span",`${bytes(p.bytes)} / ${bytes(p.total_bytes)}`,"cell-subtitle"));}
        if (task.result_summary) progress.append(node("span",`${t("resultSucceeded")}: ${task.result_summary.succeeded} / ${task.result_summary.total}`,"cell-subtitle"));
      } else if (task.copy_progress) {
        progress.className = "progress copy-progress";
        copyProgressLines(task).forEach((line, index) => progress.append(node("span", line, index ? "cell-subtitle" : "")));
        if (task.copy_progress.stage === "forwarding" && ["running", "cancelling"].includes(task.status)) {
          const p = task.copy_progress, meter = node("progress");
          meter.max = Math.max(1, p.matched); meter.value = p.processed;
          meter.setAttribute("aria-label", t("copyForwarding")); progress.append(meter);
        }
      } else if (task.result_summary && !task.downloading) {
        const counts = task.result_summary;
        progress.append(node("span", `${t("resultSucceeded")}: ${counts.succeeded ?? "—"} / ${counts.total ?? "—"}`),
          node("span", `${t(task.type === "copy" ? "resultFailed" : "resultUnsaved")}: ${counts.failed ?? "—"}`, "cell-subtitle"));
        for (const [field, label] of [["cancelled", "cancelledItems"], ["interrupted", "interruptedItems"], ["pending", "unattemptedItems"], ["running", "runningItems"]]) {
          if (counts[field] > 0) progress.append(node("span", `${t(label)}: ${counts[field]}`, "cell-subtitle"));
        }
        progress.append(node("span", t(task.type === "copy" ? "copyCountUnit" : "fileCountUnit"), "cell-subtitle"));
      } else if (task.progress && (task.progress.total_bytes || task.progress.total_files)) {
        const percent = Math.min(100, Math.max(0, Number(task.progress.percent || 0)));
        const meter = node("progress"); meter.max = 100; meter.value = percent;
        meter.setAttribute("aria-label", `${t("progress")} ${percent.toFixed(1)}%`);
        const speed = task.progress.speed_mbps === undefined ? "" : ` · ${Number(task.progress.speed_mbps).toFixed(1)} MB/s`;
        progress.append(node("span", `${task.downloading ? t("downloadProgress") + ": " : ""}${percent.toFixed(1)}%`), meter, node("span", `${bytes(task.progress.downloaded_bytes)}${speed}`, "cell-subtitle"));
      } else { progress.textContent = task.downloading ? t("upload_preparing") : "—"; }
      const actions = node("div", null, "table-actions");
      actions.append(button(t("details"), () => taskDetails(task)));
      const label = task.can_delete ? "deleteTask" : task.status === "cancelling" ? "cancelling" : "cancel";
      const action = button(t(taskActions.get(task.task_id) || label), () => taskAction(task, action), "danger-button small-button");
      action.dataset.taskAction = task.task_id;
      action.dataset.label = label;
      action.dataset.available = String(task.can_delete || task.can_cancel);
      action.disabled = taskActions.has(task.task_id) || (!task.can_delete && !task.can_cancel);
      actions.append(action);
      return [title, taskStatus(task), progress, task.storage || "—", date(task.created_at), actions];
    }));
  }
  function storageSelectors() {
    for (const id of ["target-storage", "file-storage"]) {
      const select = $(id), selected = select.value;
      const storages = id === "file-storage" ? state.storages.filter((storage) => storage.listable) : state.storages;
      select.replaceChildren();
      storages.forEach((storage) => { const option = node("option", storage.name); option.value = storage.name; select.append(option); });
      if (storages.some((storage) => storage.name === selected)) select.value = selected;
      select.disabled = !storages.length;
    }
  }
  function taskTypes() {
    const selected = $("task-type").value;
    $("task-type").replaceChildren();
    state.capabilities.forEach((capability) => {
      const option = node("option", `${t(capability.type)}${capability.available ? "" : ` · ${t("unavailable")}`}`);
      option.value = capability.type; option.disabled = !capability.available;
      $("task-type").append(option);
    });
    const valid = state.capabilities.find((capability) => capability.type === selected && capability.available);
    $("task-type").value = valid ? selected : (state.capabilities.find((capability) => capability.available)?.type || "");
    typeFields();
  }
  function storagePathField() {
    const collection=state.storages.find((store)=>store.name===$("target-storage").value)?.type==="telegram";
    $("target-path").hidden=collection;$("target-path").disabled=collection;
    document.querySelector('label[for="target-path"]').hidden=collection;
  }
  $("target-storage").addEventListener("change",storagePathField);
  function typeFields() {
    storagePathField();
    const type = $("task-type").value;
    $("task-urls").required = true;
    $("urls-label").textContent = t("messageLinks");
    $("task-type-note").textContent = t("urlsNote");
    $("task-submit").disabled = state.submitting || !type || !state.storages.length;
  }
  function capabilityList() {
    $("overview-capabilities").replaceChildren();
    state.capabilities.forEach((capability) => {
      const row = node("div", null, "capability-row");
      row.append(node("span", t(capability.type)), badge(capability.available ? "available" : "unavailable"));
      $("overview-capabilities").append(row);
    });
  }
  function storageCards() {
    $("storage-cards").replaceChildren();
    if (!state.storages.length) { $("storage-cards").append(node("p", t("empty"), "empty")); return; }
    state.storages.forEach((storage) => {
      const card = node("article", null, "panel storage-card");
      card.append(node("h2", storage.name), node("span", storage.type, "badge"));
      const values = node("dl", null, "definition-list");
      definitions(values, [[t("readable"), storage.readable], [t("listable"), storage.listable], [t("stream"), storage.stream], [t("detectExistence"), storage.detect_existence]]);
      if (storage.type === "telegram") {
        definitions(values,[[t("collectionTarget"),`${storage.chat_id}${storage.topic_id ? ` Topic:${storage.topic_id}` : ""}`]]);
        card.append(node("p",t("collectionNote"),"small muted"));
      }
      card.append(values);
      if (storage.listable) card.append(button(t("browseFiles"), () => {
        $("file-storage").value = storage.name; $("file-path").value = "/"; state.fileOffset = 0; location.hash = "files";
      }));
      if (storage.type === "shardreel") {
        const check = button(t("checkConnection"), async () => {
          check.disabled = true;
          try { const result = await request(`storages/${encodeURIComponent(storage.name)}/check`, { method: "POST" }); toast(`${t("connectionReady")}: ${result.root}`); }
          catch (error) { toast(error.message); } finally { check.disabled = false; }
        });
        card.append(check);
      }
      $("storage-cards").append(card);
    });
  }
  function taskStatus(task) {
    const value = node("div");
    const key = taskStatusKey(task);
    value.append(node("span", t(key), `badge ${key === "executionEnded" ? "ended" : task.status}`));
    if (task.copy_outcome) value.append(node("div", t(`copyOutcome_${task.copy_outcome}`), `cell-subtitle ${["failed", "partial"].includes(task.copy_outcome) ? "result-warning" : ""}`));
    return value;
  }
  function taskStatusKey(task) {
    if (task.downloading && task.status === "running") return "upload_preparing";
    if (task.upload_files?.length) {
      if (task.status === "completed") {
        if (task.upload_files.every((f) => f.status === "skipped")) return "upload_skipped";
        return task.upload_files.some((f) => !f.local_clean) ? "upload_saved_cleanup" : "upload_saved";
      }
      if (task.status === "failed") return "upload_attention";
      if (task.status === "cancelled" || task.status === "cancelling") return task.status;
      const file = task.upload_files.find((f) => ["uploading", "processing", "cancelling"].includes(f.status)) || task.upload_files.find((f) => ["preparing", "queued", "retry_wait"].includes(f.status));
      if (file) return `upload_${file.status}`;
    }
    const incomplete = task.result_summary && task.result_summary.succeeded < task.result_summary.total;
    return task.status === "completed" && (incomplete || (task.type === "copy" && task.copy_outcome !== "succeeded")) ? "executionEnded" : task.status;
  }
  function userSelectors(users) {
    state.users = users;
    for (const id of ["copy-user", "watch-user", "preview-user"]) {
      const select = $(id), selected = select.value;
      select.replaceChildren();
      users.forEach((user) => { const option = node("option", user.chat_id); option.value = String(user.chat_id); select.append(option); });
      if (users.some((user) => String(user.chat_id) === selected)) select.value = selected;
      select.disabled = !users.length;
    }
    management.usersChanged(users, state.page);
  }
  function preferenceEditor(user) {
    const form = node("form", null, "preference-editor");
    const controls = [];
    const add = (key, label, value, choices) => {
      const input = choices ? node("select") : node("input");
      input.id = `preference-${user.chat_id}-${key}`;
      if (choices) choices.forEach(([value, text]) => { const option = node("option", text); option.value = String(value); input.append(option); });
      else input.maxLength = 4096;
      input.value = String(value ?? "");
      input.dataset.original = input.value;
      const caption = node("label", t(label)); caption.htmlFor = input.id;
      form.append(caption, input); controls.push([key, input]); return input;
    };
    const yesNo = [["true", t("yes")], ["false", t("no")]];
    add("silent", "silent", user.silent, yesNo);
    add("apply_rule", "applyRules", user.apply_rule, yesNo);
    const storages = state.storages.map((item) => [item.name, item.name]);
    if (!storages.some(([name]) => name === user.default_storage)) storages.unshift([user.default_storage, user.default_storage || t("unset")]);
    const storage = add("default_storage", "defaultStorage", user.default_storage, storages);
    const dir = add("default_dir", "defaultDirectory", user.default_dir || 0, []);
    function directoryChoices() {
      const collection=state.storages.find((store)=>store.name===storage.value)?.type==="telegram";
      dir.disabled=collection;dir.hidden=collection;form.querySelector(`label[for="${dir.id}"]`).hidden=collection;
      dir.replaceChildren();
      [[0, t("storageRoot")], ...user.directories.filter((item) => item.storage === storage.value).map((item) => [item.id, item.path])].forEach(([value, label]) => {
        const option = node("option", label); option.value = String(value); dir.append(option);
      });
    }
    directoryChoices(); dir.value = String(user.default_dir || 0); dir.dataset.original = dir.value;
    storage.addEventListener("change", directoryChoices);
    add("filename_strategy", "filenameStrategy", user.filename_strategy || "default", [["default", t("namingDefault")], ["message", t("namingMessage")], ["template", t("namingTemplate")]]);
    add("filename_template", "filenameTemplate", user.filename_template);
    add("conflict_strategy", "conflictStrategy", user.conflict_strategy || "rename", ["rename", "skip", "overwrite", "ask"].map((value) => [value, t(`conflict_${value}`)]));
    const save = node("button", t("savePreferences"), "primary"); save.type = "submit"; form.append(save);
    form.addEventListener("submit", async (event) => {
      event.preventDefault(); if (save.disabled) return;
      const payload = { user_chat_id: user.chat_id };
      for (const [key, input] of controls) if (input.value !== input.dataset.original) payload[key] = ["silent", "apply_rule"].includes(key) ? input.value === "true" : key === "default_dir" ? Number(input.value) : input.value;
      if (Object.keys(payload).length === 1) { toast(t("noChanges")); return; }
      save.disabled = true;
      try { await request("preferences", { method: "PATCH", body: JSON.stringify(payload) }); toast(t("saved")); await loadPage(); }
      catch (error) { toast(error.message); } finally { save.disabled = false; }
    });
    return form;
  }
  const preferences = SaveAnyPreferences.init({ $, t, node, button, table, request, toast, storages: () => state.storages, reload: () => loadPage() });
  function userPreferences(users, presets) {
    $("preference-cards").replaceChildren();
    if (!users.length) { $("preference-cards").append(node("p", t("empty"), "empty")); return; }
    users.forEach((user) => {
      const panel = node("article", null, "panel"); panel.append(node("h2", `${t("telegramUser")} ${user.chat_id}`));
      const values = node("dl", null, "definition-list");
      const defaultDir = user.directories.find((directory) => directory.id === user.default_dir);
      const collection=state.storages.find((store)=>store.name===user.default_storage)?.type==="telegram";
      if(collection) panel.append(node("p",t("collectionNote"),"note"));
      definitions(values, [[t("defaultStorage"), user.default_storage || t("unset")], [t("defaultDirectory"), collection ? t("notApplicable") : defaultDir?.path || t("storageRoot")],
        [t("silent"), user.silent], [t("applyRules"), user.apply_rule], [t("filenameStrategy"), user.filename_strategy || t("unset")],
        [t("filenameTemplate"), user.filename_template || t("unset")], [t("conflictStrategy"), user.conflict_strategy || t("unset")]]);
      panel.append(values, preferenceEditor(user));
      preferences.render(panel, user, presets); $("preference-cards").append(panel);
    });
  }
  function userRoutes(users) {
    $("route-table").replaceChildren();
    if (!users.length) { $("route-table").append(node("p", t("empty"), "empty")); return; }
    users.forEach((user) => {
      const panel = node("article", null, "panel"); panel.append(node("h2", `${t("telegramUser")} ${user.chat_id}`));
      const target = node("div");
      table(target, ["sourceChat", "targetChat", "topic", "filter", "actions"], user.routes.map((route) =>
        [`${route.source || "—"} (${route.source_id})`, route.target_id ? `${route.target || "—"} (${route.target_id})` : t("localSaving"),
          route.topic_id ? `${route.topic || "—"} (${route.topic_id})` : "—", route.filter || t("none"), stopRouteButton(user.chat_id, route)]));
      panel.append(target); $("route-table").append(panel);
    });
  }
  function fileTable(data) {
    $("file-path").value = data.path;
    table($("file-table"), ["name", "size", "modifiedAt", "actions"], data.files.map((file) => {
      const title = node("div", null, "cell-title"); title.append(node("span", `${file.is_dir ? "▱" : "▤"} ${file.name}`));
      let action;
      if (file.is_dir) action = button(t("openDirectory"), () => {
        $("file-path").value = file.path; state.fileOffset = 0; loadPage();
      });
      else {
        action = node("a", t("download"), "button secondary small-button");
        action.href = `/admin/api/v1/download?${new URLSearchParams({ storage: $("file-storage").value, path: file.path })}`;
        action.setAttribute("download", file.name);
      }
      return [title, file.is_dir ? "—" : bytes(file.size), date(file.modified_at), action];
    }));
    pagination($("file-pagination"), data.total, data.offset, (offset) => { state.fileOffset = offset; loadPage(); });
  }
  async function loadPage() {
    if (!state.authenticated) return;
    clearTimeout(refreshTimer);
    const sequence = ++state.sequence;
    if (state.abort) state.abort.abort();
    state.abort = new AbortController();
    const signal = state.abort.signal;
    state.loading = true;
    const page = pages.includes(location.hash.slice(1)) ? location.hash.slice(1) : "overview";
    state.page = page;
    if (page !== "preferences") preferences.close();
    $("refresh").disabled = true;
    $(`page-${page}`).setAttribute("aria-busy", "true");
    $("page-title").textContent = t(page);
    pages.forEach((key) => { $(`page-${key}`).hidden = key !== page; });
    document.querySelectorAll("[data-page]").forEach((element) => {
      element.classList.toggle("active", element.dataset.page === page);
      if (element.dataset.page === page) element.setAttribute("aria-current", "page"); else element.removeAttribute("aria-current");
    });
    $("page-error").hidden = true;
    try {
      if (!state.metadata) {
        const [storages, types] = await Promise.all([request("storages", { signal }), request("task-types", { signal })]);
        if (sequence !== state.sequence) return;
        state.storages = storages.storages; state.capabilities = types.capabilities; state.metadata = true;
        storageSelectors(); taskTypes();
      }
      if (page === "overview") {
        const [overview, tasks] = await Promise.all([request("overview", { signal }), request("tasks?limit=5", { signal })]);
        if (sequence !== state.sequence) return;
        state.hasActiveTasks = tasks.has_active_tasks;
        $("overview-metrics").replaceChildren();
        [["runningTasks", overview.running, "workerNote"], ["queuedTasks", overview.queued, "queueNote"], ["loadedStorages", overview.storages, "loadedNote"], ["consoleUptime", duration(overview.uptime_seconds), "uptimeNote"]].forEach(([label, value, subtitle]) => {
          const metric = node("article", null, "metric"); metric.append(node("span", t(label), "metric-label"), node("div", value, "metric-value"), node("span", t(subtitle), "metric-subtitle")); $("overview-metrics").append(metric);
        });
        definitions($("service-information"), [[t("version"), overview.version], [t("commit"), overview.commit], [t("buildTime"), overview.build_time], [t("platform"), overview.platform], [t("goVersion"), overview.go_version], [t("workers"), overview.workers]]);
        $("outbox-panel").hidden = !overview.outboxes.length;
        table($("outbox-summary"), ["storage", "outboxPending", "outboxFailed", "outboxUsage"], overview.outboxes.map((outbox) =>
          [outbox.storage, outbox.pending_files, outbox.failed_files, `${bytes(outbox.reserved_bytes)} / ${bytes(outbox.max_pending_bytes)}`]));
        capabilityList(); tasksTable($("overview-tasks"), tasks.tasks);
      } else if (page === "tasks") {
        const data = await request(`tasks?${new URLSearchParams({ q: $("task-search").value, status: $("task-status").value, offset: String(state.taskOffset) })}`, { signal });
        if (sequence !== state.sequence) return;
        state.hasActiveTasks = data.has_active_tasks;
        state.taskOffset = data.offset;
        tasksTable($("task-table"), data.tasks);
        pagination($("task-pagination"), data.total, data.offset, (offset) => { state.taskOffset = offset; loadPage(); });
      } else if (page === "storages") { storageCards(); }
      else if (page === "new") { storageSelectors(); taskTypes(); const data = await request("preferences", { signal }); if (sequence !== state.sequence) return; userSelectors(data.users); }
      else if (page === "files") {
        if (!$("file-storage").value) { $("file-table").replaceChildren(node("p", t("noLocalStorage"), "empty")); $("file-pagination").replaceChildren(); }
        else {
          const data = await request(`files?${new URLSearchParams({ storage: $("file-storage").value, path: $("file-path").value, offset: String(state.fileOffset) })}`, { signal });
          if (sequence !== state.sequence) return; fileTable(data);
        }
      } else if (page === "routes" || page === "preferences") {
        const data = await request("preferences", { signal });
        if (sequence !== state.sequence) return;
        userSelectors(data.users); if (page === "routes") userRoutes(data.users); else userPreferences(data.users, data.rule_presets);
      } else if (page === "diagnostics") {
        const data = await request("diagnostics", { signal });
        if (sequence !== state.sequence) return;
        definitions($("runtime-settings"), Object.entries(data.settings).map(([key, value]) => [t(`setting_${key}`), value]));
        definitions($("watch-health"), [[t("watchPending"), data.watch?.Pending], [t("watchBuffered"), data.watch?.Buffered], [t("watchCapacity"), data.watch?.Capacity], [t("watchEnqueued"), data.watch?.Enqueued], [t("watchDropped"), data.watch?.Dropped], [t("botInitialized"), data.telegram?.bot_initialized], [t("userbotInitialized"), data.telegram?.userbot_initialized]]);
        const forward = data.watch_forward;
        definitions($("forward-health"), [[t("forwardPending"), forward?.Pending], [t("forwardRunning"), forward?.Running],
          [t("forwardCapacity"), forward?.Capacity], [t("forwardWorkers"), forward?.Workers],
          [t("forwardAccepted"), forward?.Accepted], [t("forwardStarted"), forward?.Started],
          [t("forwardSucceeded"), forward?.Succeeded], [t("forwardFailed"), forward?.Failed],
          [t("forwardCancelled"), forward?.Cancelled], [t("forwardInterrupted"), forward?.Interrupted],
          [t("forwardRejectedFull"), forward?.RejectedFull], [t("forwardRejectedClosed"), forward?.RejectedClosed],
          [t("forwardRejectedUnavailable"), forward?.RejectedUnavailable], [t("forwardDiscardedStopped"), forward?.DiscardedStopped],
          [t("forwardDiscardedShutdown"), forward?.DiscardedShutdown], [t("forwardClosed"), forward?.Closed]]);
        table($("disk-health"), ["storage", "availableDisk"], (data.disks || []).map((disk) => [disk.storage, disk.available ? bytes(disk.available_bytes) : t("unavailable")]));
        table($("scan-health"), ["scanMode", "scanPages", "scanSucceeded", "scanFailed", "scanTimedOut", "scanCancelled", "scanLastDuration", "scanMaxDuration"], (data.scans || []).map((scan) => [t(`scan_${scan.mode}`), scan.pages, scan.succeeded, scan.failed, scan.timed_out, scan.cancelled, scan.pages ? Number(scan.last_duration_ms).toFixed(1) + " ms" : t("scanNoSamples"), scan.pages ? Number(scan.max_duration_ms).toFixed(1) + " ms" : t("scanNoSamples")]));
        table($("audit-table"), ["time", "operation", "object", "result"], data.audit.map((entry) => [date(entry.time), t(`audit_${entry.action}`), entry.object || "—", t(entry.result)]));
      }
      if (sequence === state.sequence) $("last-refresh").textContent = `${t("lastRefreshed")} ${new Date().toLocaleTimeString(state.language)}`;
    } catch (error) {
      if (error.name !== "AbortError" && sequence === state.sequence && state.authenticated) {
        state.hasActiveTasks = true;
        $("page-error").textContent = error.message; $("page-error").hidden = false;
      }
    } finally {
      if (sequence === state.sequence) {
        state.loading = false; $("refresh").disabled = false; $(`page-${page}`).removeAttribute("aria-busy");
        if (["overview", "tasks"].includes(page)) refreshTimer = setTimeout(refreshTasks, state.hasActiveTasks ? 3000 : 15000);
      }
    }
  }
  function stopRouteButton(userID, route) {
    const trigger = button(t("stopRoute"), async () => {
      if (!window.confirm(t("confirmStopRoute"))) return;
      trigger.disabled = true;
      try { await request(`routes/${route.id}/stop`, { method: "POST", body: JSON.stringify({ user_chat_id: userID }) }); toast(t("routeStopped")); await loadPage(); }
      catch (error) { toast(error.message); } finally { trigger.disabled = false; }
    }, "danger-button small-button");
    return trigger;
  }
  const management = SaveAnyManagement.init({ $, t, node, button, request, toast,
    authenticated: () => state.authenticated,
    copyAccepted: () => { state.taskOffset = 0; $("task-search").value = ""; $("task-status").value = ""; location.hash = "tasks"; },
    watchUpdated: () => loadPage()
  });
  const newTabs = [...document.querySelectorAll('[role="tab"][data-new-mode]')];
  function switchNewMode(mode) {
    for (const tab of newTabs) {
      const selected = tab.dataset.newMode === mode;
      tab.setAttribute("aria-selected", String(selected)); tab.tabIndex = selected ? 0 : -1;
      $(tab.getAttribute("aria-controls")).hidden = !selected;
    }
  }
  newTabs.forEach((tab, index) => {
    tab.addEventListener("click", () => switchNewMode(tab.dataset.newMode));
    tab.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      const next = event.key === "Home" ? 0 : event.key === "End" ? newTabs.length - 1 : (index + (event.key === "ArrowRight" ? 1 : -1) + newTabs.length) % newTabs.length;
      switchNewMode(newTabs[next].dataset.newMode); newTabs[next].focus();
    });
  });
  for (const prefix of ["copy", "watch"]) $(`${prefix}-user`).addEventListener("change", () => management.usersChanged(state.users, state.page));
  $("preview-form").addEventListener("submit", async (event) => {
    event.preventDefault(); const trigger = event.submitter; if (trigger.disabled) return; trigger.disabled = true;
    const payload = { user_chat_id: Number($("preview-user").value), filter: $("preview-filter").value, text: $("preview-text").value, template: $("preview-template").value, original_name: $("preview-original").value };
    try { const result = await request("preview", { method: "POST", body: JSON.stringify(payload) }); $("preview-result").textContent = `${t(result.matches ? "filterMatched" : "filterNotMatched")} · ${result.filename}`; }
    catch (error) { toast(error.message); } finally { trigger.disabled = false; }
  });
  $("login-form").addEventListener("submit", async (event) => {
    event.preventDefault(); $("login-submit").disabled = true; $("login-error").hidden = true;
    try {
      const data = await request("login", { method: "POST", body: JSON.stringify({ username: "admin", password: $("password").value }) });
      $("password").value = ""; state.csrf = data.csrf_token; state.authenticated = true;
      $("login-view").hidden = true; $("console").hidden = false; await loadPage();
    } catch (error) { $("login-error").textContent = error.message; $("login-error").hidden = false; }
    finally { $("login-submit").disabled = false; }
  });
  $("logout").addEventListener("click", async () => {
    $("logout").disabled = true;
    try { await request("logout", { method: "POST" }); showLogin(); }
    catch (error) { if (state.authenticated) toast(error.message); }
    finally { $("logout").disabled = false; }
  });
  $("task-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    if (state.submitting) return;
    const type = $("task-type").value, storage = $("target-storage").value, targetPath = $("target-path").disabled ? "" : $("target-path").value;
    const urls = $("task-urls").value.split(/\r?\n/).map((value) => value.trim()).filter(Boolean);
    if (!urls.length) { toast(t("enterUrls")); return; }
    const payload = { type, storage, path: targetPath, params: { message_links: urls } };
    const session = state.csrf;
    const fields = [...$("task-form").querySelectorAll("input,textarea,select,button")].map((el) => [el, el.disabled]);
    state.submitting = true;
    fields.forEach(([el]) => { el.disabled = true; });
    $("task-submission-results").replaceChildren();
    try {
      const response = await request("tasks", { method: "POST", body: JSON.stringify(payload) });
      if (!state.authenticated || session !== state.csrf) return;
      toast(`${t("taskAccepted")} · ${response.task_id}`); $("task-urls").value = "";
      state.taskOffset = 0; $("task-search").value = ""; $("task-status").value = "";
      if (location.hash === "#tasks") await loadPage(); else location.hash = "tasks";
      if ((response.input_results || []).some((item) => item.status !== "ready" || item.album_fallback === true) || response.input_results_omitted) selectionDetails(response, t("taskAccepted") + " · " + response.task_id);
    } catch (error) {
      if (state.authenticated && session === state.csrf) {
        toast(error.message);
        if (!error.status || error.status >= 500) {
          $("task-submission-results").append(node("p", t("taskSubmissionUnknown"), "error"),
            button(t("viewAll"), () => { location.hash = "tasks"; }));
        } else if (error.selection) selectionDetails(error.selection, error.message);
      }
    } finally {
      state.submitting = false;
      fields.forEach(([el, disabled]) => { el.disabled = disabled; });
      typeFields();
    }
  });
  $("task-type").addEventListener("change", typeFields);
  $("task-search").addEventListener("input", () => { clearTimeout(searchTimer); searchTimer = setTimeout(() => { state.taskOffset = 0; loadPage(); }, 350); });
  $("task-status").addEventListener("change", () => { state.taskOffset = 0; loadPage(); });
  $("file-form").addEventListener("submit", (event) => { event.preventDefault(); state.fileOffset = 0; loadPage(); });
  $("file-storage").addEventListener("change", () => { $("file-path").value = "/"; state.fileOffset = 0; loadPage(); });
  $("file-up").addEventListener("click", () => { const segments = $("file-path").value.replace(/\\/g, "/").split("/").filter(Boolean); segments.pop(); $("file-path").value = `/${segments.join("/")}`; state.fileOffset = 0; loadPage(); });
  $("close-input-dialog").addEventListener("click", () => $("input-dialog").close());
  $("close-dialog").addEventListener("click", () => $("task-dialog").close());
  $("refresh").addEventListener("click", () => { state.metadata = false; loadPage(); });
  window.addEventListener("hashchange", loadPage);
  // Leave focused row controls and the detail dialog stable while they are in use.
  function refreshTasks() {
    if (!state.authenticated || !["overview", "tasks"].includes(state.page)) return;
    if (!state.loading && !document.hidden && !$("task-dialog").open && !document.activeElement?.closest(".table-wrap")) loadPage();
    else refreshTimer = setTimeout(refreshTasks, 3000);
  }
  async function boot() {
    await loadLanguage(navigator.language.startsWith("zh") ? "zh-Hans" : "en");
    try {
      const session = await request("session"); state.csrf = session.csrf_token; state.authenticated = true;
      $("boot").hidden = true; $("login-view").hidden = true; $("console").hidden = false; await loadPage();
    } catch { showLogin(); }
  }
  boot().catch(() => { $("boot").textContent = "SaveAny · Unable to load the console. Reload to retry."; });
})();
