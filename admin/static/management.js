"use strict";

(() => {
  const keywordLines = (text) => [...new Set(text.split(/\r?\n/).map((s) => s.trim()).filter(Boolean))];
  function keywordFilter(mode, included, excluded) {
    const include = keywordLines(included), exclude = keywordLines(excluded);
    if (include.length + exclude.length > 16) throw new Error("keywordLimit");
    const term = (value) => {
      if (value.includes('"')) throw new Error("keywordQuote");
      return `"${value}"`;
    };
    const parts = [];
    if (include.length) parts.push(`(${include.map(term).join(mode === "all" ? "&" : "|")})`);
    if (exclude.length) parts.push(`!(${exclude.map(term).join("|")})`);
    if (new TextEncoder().encode(parts.join("&")).length > 256) throw new Error("filterTooLong");
    return parts.length ? "msgre:" + parts.join("&") : "";
  }

  function matchingChats(chats, search) {
    const query = search.trim().toLocaleLowerCase();
    return [...chats].filter((chat) => `${chat.title} ${chat.id}`.toLocaleLowerCase().includes(query))
      .sort((a, b) => a.title.localeCompare(b.title) || String(a.id).localeCompare(String(b.id)));
  }

  function init({ $, t, node, button, request, toast, authenticated, copyAccepted, watchUpdated }) {
    let users = [];
    const forms = Object.fromEntries(["copy", "watch"].map((p) => [p, {
      user: "", chats: new Map(), cachedAt: null, loaded: false, loading: false, syncing: false,
      generation: 0, topicSequence: 0, topicLoading: false, previewLoading: false, busy: false,
      sourceOptions: [], sourceActive: -1
    }]));
    const option = (label, value) => { const o = node("option", label); o.value = value; return o; };
    const radio = (name) => document.querySelector(`input[name="${name}"]:checked`)?.value || "";
    const destination = (p) => p === "watch" && radio("watch-destination") === "local" ? "0" : $(`${p}-target`).value;
    const filter = (p) => keywordFilter(radio(`${p}-filter-mode`), $(`${p}-keywords`).value, $(`${p}-exclude`).value);
    function filterSummary(p) {
      const include = keywordLines($(`${p}-keywords`).value), exclude = keywordLines($(`${p}-exclude`).value);
      return [include.length ? `${t(radio(`${p}-filter-mode`) === "all" ? "keywordAll" : "keywordAny")}: ${include.join("、")}` : "",
        exclude.length ? `${t("excludedKeywords")}: ${exclude.join("、")}` : ""].filter(Boolean).join(" · ") || t("noFiltering");
    }
    function showFilterError(p, message) {
      $(`${p}-filter-error`).textContent = message;
      $(`${p}-filter-error`).hidden = !message;
      if (message) $(`${p}-filter-details`).open = true;
    }
    function updateFilter(p) {
      $(`${p}-filter-summary`).textContent = filterSummary(p);
      try { filter(p); showFilterError(p, ""); }
      catch (error) { showFilterError(p, t(error.message)); }
    }
    function localDestination() {
      const user = users.find((u) => String(u.chat_id) === forms.watch.user);
      const directory = user?.directories.find((d) => d.id === user.default_dir);
      return user?.default_storage ? `${t("defaultStorage")}: ${user.default_storage} · ${t("defaultDirectory")}: ${directory?.path || t("storageRoot")}` : t("localStorageMissing");
    }
    function localSettings() {
      const local = destination("watch") === "0", user = users.find((u) => String(u.chat_id) === forms.watch.user);
      $("watch-local-settings").hidden = !local;
      $("watch-local-settings").classList.toggle("missing", !user?.default_storage);
      $("watch-local-path").textContent = localDestination();
      $("watch-local-rules").hidden = !user?.apply_rule || !user.rules.length;
    }

    function closeSource(p) {
      $(`${p}-source-options`).hidden = true;
      $(`${p}-source-input`).setAttribute("aria-expanded", "false");
      $(`${p}-source-input`).removeAttribute("aria-activedescendant");
    }
    function highlightSource(p, index) {
      const f = forms[p], input = $(`${p}-source-input`), rows = $(`${p}-source-options`).querySelectorAll('[role="option"]');
      f.sourceActive = index;
      rows.forEach((row, i) => row.classList.toggle("active", i === index));
      if (rows[index]) { input.setAttribute("aria-activedescendant", rows[index].id); rows[index].scrollIntoView({ block: "nearest" }); }
      else input.removeAttribute("aria-activedescendant");
    }
    function chooseSource(p, chat) {
      $(`${p}-source`).value = String(chat.id);
      $(`${p}-source-input`).value = `${chat.title} (${chat.id})`;
      $(`${p}-source-input`).setCustomValidity("");
      closeSource(p); renderTargets(p);
    }
    function sourceOptions(p, query) {
      const f = forms[p], list = $(`${p}-source-options`);
      f.sourceOptions = matchingChats(f.chats.values(), query); list.replaceChildren();
      f.sourceOptions.forEach((chat, i) => {
        const row = node("div", `${chat.title} (${chat.id})`, "chat-option");
        row.tabIndex = -1;
        row.id = `${p}-source-option-${i}`; row.setAttribute("role", "option");
        row.setAttribute("aria-selected", String(String(chat.id) === $(`${p}-source`).value));
        // Keep input focus on mouse selection; touch scrolling must not select.
        row.addEventListener("mousedown", (event) => event.preventDefault());
        row.addEventListener("click", () => chooseSource(p, chat));
        list.append(row);
      });
      if (!f.sourceOptions.length) list.append(node("p", t(f.loading ? f.syncing ? "syncingLists" : "loadingChats" : f.loaded ? f.cachedAt ? "noMatchingChats" : "chatCacheMissing" : "chatLoadFailed"), "chat-empty"));
      highlightSource(p, -1);
    }
    function openSource(p, query) {
      if ($(`${p}-source-input`).disabled) return;
      const opening = $(`${p}-source-options`).hidden;
      sourceOptions(p, query); $(`${p}-source-options`).hidden = false;
      $(`${p}-source-input`).setAttribute("aria-expanded", "true");
      if (opening && !forms[p].loaded) loadChats(p);
    }

    function resetTopics(p) {
      const f = forms[p]; f.topicSequence++; f.topicLoading = false;
      $(`${p}-topics`).replaceChildren(option(t("noTopic"), ""));
      $(`${p}-topics-status`).textContent = "";
    }
    function controls(p) {
      const f = forms[p], local = destination(p) === "0", locked = f.busy || !f.user;
      $(`${p}-user`).disabled = locked;
      $(`${p}-target-fields`).hidden = local;
      $(`${p}-target`).required = !local; $(`${p}-target`).disabled = local || locked;
      $(`${p}-source`).disabled = locked || f.loading;
      $(`${p}-source-input`).disabled = locked;
      $(`${p}-source-toggle`).disabled = locked;
      if (locked) closeSource(p);
      const forum = !local && !!f.chats.get(destination(p))?.forum;
      $(`${p}-topic-fields`).hidden = !forum;
      $(`${p}-topics`).disabled = !forum || f.topicLoading || locked;
      $(`${p}-submit`).disabled = locked || f.topicLoading || f.loading || f.previewLoading;
      $(`${p}-filter-test`).disabled = locked || f.previewLoading;
      $(`${p}-source-refresh`).disabled = locked || f.loading;
      $(`${p}-target-refresh`).disabled = locked || f.loading;
      $(`${p}-topics-refresh`).disabled = !forum || f.topicLoading || locked || f.loading;
      if (p === "watch") localSettings();
    }
    function renderTargets(p) {
      const f = forms[p], select = $(`${p}-target`), old = select.value;
      select.replaceChildren(option(t("chooseTarget"), ""));
      matchingChats(f.chats.values(), "").filter((c) => c.managed).forEach((chat) => {
        const o = option(`${chat.title} (${chat.id})`, String(chat.id));
        o.disabled = String(chat.id) === $(`${p}-source`).value; select.append(o);
      });
      if ([...select.options].some((o) => o.value === old && !o.disabled)) select.value = old;
      if (old && select.value !== old) resetTopics(p);
      controls(p);
    }
    function renderChats(p) {
      const f = forms[p], selected = $(`${p}-source`), input = $(`${p}-source-input`), chat = f.chats.get(selected.value);
      if (chat) input.value = `${chat.title} (${chat.id})`;
      else if (selected.value) { selected.value = ""; input.value = ""; }
      input.setCustomValidity(selected.value ? "" : t("chooseSource"));
      sourceOptions(p, selected.value ? "" : input.value);
      renderTargets(p);
    }
    const syncedAt = (value) => value ? `${t("lastSynced")}: ${new Date(value).toLocaleString(document.documentElement.lang)}` : "";
    function listStatus(p) {
      const f = forms[p];
      $(`${p}-chats-status`).textContent = f.loading ? t(f.syncing ? "syncingLists" : "loadingChats") : f.cachedAt ? `${f.chats.size} ${t("loadedChats")} · ${syncedAt(f.cachedAt)}` : t("chatCacheMissing");
      controls(p);
    }
    async function loadChats(p, refresh = false) {
      const f = forms[p]; if (f.loading || !f.user || f.busy) return;
      const generation = ++f.generation, user = f.user;
      f.loading = true; f.syncing = refresh; listStatus(p);
      if (!$(`${p}-source-options`).hidden) sourceOptions(p, $(`${p}-source`).value ? "" : $(`${p}-source-input`).value);
      let errorMessage = "";
      try {
        const data = await request(refresh ? "chats/refresh" : "chats", { method: "POST", body: JSON.stringify({ user_chat_id: Number(user) }) });
        if (f.generation !== generation || f.user !== user || !authenticated()) return;
        const oldTarget = destination(p), wasForum = f.chats.get(oldTarget)?.forum;
        f.chats = new Map(data.chats.map((chat) => [String(chat.id), chat]));
        f.cachedAt = data.updated_at; f.loaded = true;
        renderChats(p);
        if (wasForum !== f.chats.get(destination(p))?.forum) await loadTopics(p);
      } catch (error) { if (f.generation === generation) errorMessage = `${t(refresh ? "listSyncFailed" : "chatLoadFailed")} ${error.message}`; }
      finally {
        if (f.generation === generation) {
          f.loading = false; f.syncing = false; listStatus(p);
          if (errorMessage) $(`${p}-chats-status`).textContent = errorMessage;
          sourceOptions(p, $(`${p}-source`).value ? "" : $(`${p}-source-input`).value);
        }
      }
    }
    async function loadTopics(p, refresh = false) {
      const f = forms[p];
      if ((f.topicLoading && refresh) || f.busy) return;
      const previous = refresh ? $(`${p}-topics`).value : "";
      if (refresh) f.topicSequence++; else resetTopics(p);
      controls(p);
      const target = destination(p), user = f.user;
      if (target === "0" || !f.chats.get(target)?.forum) return;
      const sequence = f.topicSequence; f.topicLoading = true; controls(p);
      $(`${p}-topics-status`).textContent = t(refresh ? "syncingLists" : "loadingTopics");
      try {
        const data = await request(refresh ? "topics/refresh" : "topics", { method: "POST", body: JSON.stringify({ user_chat_id: Number(user), target }) });
        if (sequence !== f.topicSequence || user !== f.user || target !== destination(p) || !authenticated()) return;
        const select = $(`${p}-topics`); select.replaceChildren(option(t("noTopic"), ""));
        for (const topic of data.topics) select.append(option(`${topic.title} (${topic.id})`, String(topic.id)));
        if ([...select.options].some((o) => o.value === previous)) select.value = previous;
        $(`${p}-topics-status`).textContent = data.updated_at ? `${t(data.topics.length ? "topicsReady" : "noTopics")} · ${syncedAt(data.updated_at)}` : t("topicCacheMissing");
      } catch (error) {
        if (sequence === f.topicSequence) $(`${p}-topics-status`).textContent = `${t(refresh ? "listSyncFailed" : "topicLoadFailed")} ${error.message}`;
      } finally { if (sequence === f.topicSequence) { f.topicLoading = false; controls(p); } }
    }
    for (const p of ["copy", "watch"]) {
      const input = $(`${p}-source-input`);
      input.addEventListener("input", () => {
        $(`${p}-source`).value = ""; input.setCustomValidity(t("chooseSource"));
        openSource(p, input.value); renderTargets(p);
      });
      const editSource = () => {
        const selected = $(`${p}-source`).value;
        openSource(p, selected ? "" : input.value);
        if (selected) input.select();
      };
      input.addEventListener("focus", editSource);
      input.addEventListener("click", () => { if ($(`${p}-source-options`).hidden) editSource(); });
      input.addEventListener("blur", () => closeSource(p));
      input.addEventListener("keydown", (event) => {
        if (event.isComposing) return;
        const f = forms[p];
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          if ($(`${p}-source-options`).hidden) openSource(p, $(`${p}-source`).value ? "" : input.value);
          const count = f.sourceOptions.length, down = event.key === "ArrowDown";
          if (count) highlightSource(p, f.sourceActive < 0 ? down ? 0 : count - 1 : (f.sourceActive + (down ? 1 : -1) + count) % count);
        } else if (event.key === "Enter" && !$(`${p}-source-options`).hidden) {
          event.preventDefault(); const chat = f.sourceOptions[Math.max(0, f.sourceActive)]; if (chat) chooseSource(p, chat);
        } else if (event.key === "Escape") { event.preventDefault(); closeSource(p); }
      });
      $(`${p}-source-toggle`).addEventListener("mousedown", (event) => event.preventDefault());
      $(`${p}-source-toggle`).addEventListener("click", () => {
        const wasOpen = !$(`${p}-source-options`).hidden; input.focus();
        if (wasOpen) closeSource(p); else editSource();
      });
      for (const name of ["source-refresh", "target-refresh"]) $(`${p}-${name}`).addEventListener("click", () => loadChats(p, true));
      $(`${p}-target`).addEventListener("change", () => loadTopics(p));
      $(`${p}-topics-refresh`).addEventListener("click", () => loadTopics(p, true));
      for (const name of ["filter-mode", "keywords", "exclude"]) $(`${p}-${name}`).addEventListener("input", () => {
        updateFilter(p); $(`${p}-filter-result`).textContent = "";
      });
      $(`${p}-sample`).addEventListener("input", () => { $(`${p}-filter-result`).textContent = ""; });
      $(`${p}-filter-test`).addEventListener("click", async () => {
        const trigger = $(`${p}-filter-test`), f = forms[p]; if (trigger.disabled || f.busy) return;
        f.previewLoading = true; controls(p);
        const user = f.user, generation = f.generation, value = $(`${p}-sample`).value;
        const mode = radio(`${p}-filter-mode`), included = $(`${p}-keywords`).value, excluded = $(`${p}-exclude`).value;
        const current = () => authenticated() && user === f.user && generation === f.generation && value === $(`${p}-sample`).value &&
          mode === radio(`${p}-filter-mode`) && included === $(`${p}-keywords`).value && excluded === $(`${p}-exclude`).value;
        try {
          const expression = filter(p);
          const data = await request("preview", { method: "POST", body: JSON.stringify({ user_chat_id: Number(user), filter: expression, text: value, template: "{{.origname}}", original_name: "sample" }) });
          if (current()) $(`${p}-filter-result`).textContent = t(data.matches ? "filterMatched" : "filterNotMatched");
        } catch (error) { if (current()) $(`${p}-filter-result`).textContent = t(error.message); }
        finally { f.previewLoading = false; controls(p); }
      });
      $(`${p}-form`).addEventListener("submit", async (event) => {
        event.preventDefault(); const f = forms[p]; if (f.busy || f.loading || f.topicLoading || f.previewLoading) return;
        const source = $(`${p}-source`).value, target = destination(p);
        if (!source) { toast(t("chooseSource")); return; }
        if (!target || source === target) { toast(t("chooseDifferentTarget")); return; }
        let expression; try { expression = filter(p); } catch (error) {
          showFilterError(p, t(error.message));
          const field = $(`${p}-keywords`).value.trim() ? "keywords" : "exclude";
          $(`${p}-${field}`).focus(); return;
        }
        const owner = users.find((u) => String(u.chat_id) === f.user);
        if (target === "0" && !owner?.default_storage) {
          toast(t("localStorageMissing"));
          $("watch-local-settings-link").focus(); return;
        }
        const topic = target !== "0" && f.chats.get(target)?.forum ? $(`${p}-topics`).value : "";
        const count = Number($("copy-count").value), user = f.user, generation = f.generation;
        const sourceLabel = f.chats.get(source)?.title || source, targetLabel = target === "0" ? t("saveLocally") : f.chats.get(target)?.title || target;
        const topicLine = target !== "0" && f.chats.get(target)?.forum ? `\n${topic ? $(`${p}-topics`).selectedOptions[0].textContent : t("noTopic")}` : "";
        const localLine = target === "0" ? `\n${localDestination()}${owner?.apply_rule && owner.rules.length ? `\n${t("localRulesNote")}` : ""}` : "";
        if (!window.confirm(`${t(p === "copy" ? "confirmCreateCopy" : "confirmCreateRoute")}\n${sourceLabel} → ${targetLabel}${topicLine}${localLine}\n${filterSummary(p)}${p === "copy" ? `\n${t("copyCount")}: ${count}` : ""}`)) return;
        const fields = [...$(`${p}-form`).querySelectorAll("input,textarea,select,button")].map((el) => [el, el.disabled]);
        f.busy = true; fields.forEach(([el]) => { el.disabled = true; }); $(`${p}-submission-results`).replaceChildren();
        const payload = { user_chat_id: Number(user), source, target: topic ? `${target}:${topic}` : target, filter: expression };
        if (p === "copy") payload.count = count;
        try {
          const data = await request(p === "copy" ? "copies" : "routes", { method: "POST", body: JSON.stringify(payload) });
          if (!authenticated() || user !== f.user || generation !== f.generation) return;
          toast(`${t(p === "copy" ? "taskAccepted" : "routeCreated")} · ${p === "copy" ? data.task_id : data.id}`);
          if (p === "copy") copyAccepted(); else await watchUpdated();
        } catch (error) {
          if (authenticated() && user === f.user && generation === f.generation) {
            const uncertain = !error.status || error.status >= 500;
            $(`${p}-submission-results`).append(node("p", `${t(uncertain ? p === "copy" ? "copySubmissionUnknown" : "submissionUnknown" : "submissionRejected")} · ${error.message}`, "error"));
            if (uncertain && p === "copy") $(`${p}-submission-results`).append(button(t("viewAll"), copyAccepted));
          }
        } finally { f.busy = false; fields.forEach(([el, disabled]) => { el.disabled = disabled; }); controls(p); }
      });
    }
    document.addEventListener("pointerdown", (event) => {
      for (const p of ["copy", "watch"]) if (!$(`${p}-source-control`).contains(event.target)) closeSource(p);
    });
    $("watch-destination").addEventListener("change", () => loadTopics("watch"));
    return {
      usersChanged(updatedUsers, page) {
        users = updatedUsers;
        for (const p of ["copy", "watch"]) {
          const f = forms[p], user = $(`${p}-user`).value;
          if (user !== f.user) {
            f.user = user; f.generation++; f.chats.clear(); f.cachedAt = null; f.loaded = false; f.loading = false;
            $(`${p}-source`).value = ""; $(`${p}-source-input`).value = ""; closeSource(p); resetTopics(p); $(`${p}-submission-results`).replaceChildren();
          }
          renderChats(p); listStatus(p); $(`${p}-topics`).options[0].textContent = t("noTopic");
          updateFilter(p);
          $(`${p}-user`).hidden = users.length === 1; document.querySelector(`label[for="${p}-user"]`).hidden = users.length === 1;
          if ((p === "copy" && page === "new") || (p === "watch" && page === "routes")) loadChats(p);
        }
      },
      logout() {
        users = [];
        for (const p of ["copy", "watch"]) {
          const f = forms[p]; f.user = ""; f.generation++; f.chats.clear(); f.cachedAt = null; f.loaded = false; f.loading = false;
          $(`${p}-source`).value = ""; $(`${p}-source-input`).value = ""; closeSource(p); resetTopics(p); renderChats(p); $(`${p}-submission-results`).replaceChildren();
        }
      }
    };
  }
  globalThis.SaveAnyManagement = { init, keywordFilter, matchingChats };
})();
