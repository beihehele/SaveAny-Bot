"use strict";

(() => {
  function init({ $, t, node, button, table, request, toast, storages, reload }) {
    const dialog = $("settings-dialog"), form = $("settings-form"), fields = $("settings-fields"), error = $("settings-error");
    const submit = $("settings-submit"), closeButton = $("settings-close");
    const ruleTypes = { "FILENAME-REGEX": "ruleFilename", "MESSAGE-REGEX": "ruleMessage", "IS-ALBUM": "ruleAlbum" };
    let busy = false;
    const close = () => dialog.close();
    closeButton.addEventListener("click", close);
    dialog.addEventListener("cancel", (event) => { if (busy) event.preventDefault(); });

    function ready(panel) {
      if ([...panel.querySelectorAll(".preference-editor [data-original]")].some((input) => input.value !== input.dataset.original)) {
        toast(t("savePreferencesFirst")); panel.querySelector(".preference-editor button").focus(); return false;
      }
      return true;
    }
    function field(key, label, value, choices, parent = fields) {
      const wrap = node("div"), input = node(choices ? "select" : "input"), caption = node("label", t(label));
      input.id = `setting-${key}`; caption.htmlFor = input.id;
      if (choices) choices.forEach(([value, text]) => {
        const option = node("option", text); option.value = value; input.append(option);
      });
      else input.maxLength = 4096;
      input.value = value; wrap.append(caption, input); parent.append(wrap);
      return { input, wrap };
    }
    function open(title) {
      fields.replaceChildren(); error.hidden = true; error.textContent = "";
      $("settings-heading").textContent = t(title); submit.disabled = closeButton.disabled = false;
    }
    function editing(user, endpoint, entry, values) {
      form.onsubmit = async (event) => {
        event.preventDefault(); if (busy) return;
        busy = true; submit.disabled = closeButton.disabled = true; error.hidden = true;
        try {
          await request(`${endpoint}${entry ? `/${entry.id}` : ""}`, {
            method: entry ? "PUT" : "POST", body: JSON.stringify({ user_chat_id: user.chat_id, ...values() })
          });
          close(); toast(t("saved")); await reload();
        } catch (failure) { error.textContent = failure.message; error.hidden = false; }
        finally { busy = false; submit.disabled = closeButton.disabled = false; }
      };
      dialog.showModal(); fields.querySelector("input,select")?.focus();
    }
    const storageChoices = () => storages().map((store) => [store.name, store.name]);

    function editDirectory(panel, user, entry) {
      if (!ready(panel)) return;
      open(entry ? "editDirectory" : "addDirectory");
      const storage = field("storage", "storage", entry?.storage || user.default_storage || storages()[0]?.name || "", storageChoices()).input;
      const path = field("path", "directoryPath", entry?.path || "").input;
      storage.required = path.required = true; path.placeholder = t("directoryPathExample");
      fields.append(node("p", t("directoryEditNote"), "small muted"));
      if (entry?.id === user.default_dir) fields.append(node("p", t("defaultDirectoryEditNote"), "note"));
      editing(user, "directories", entry, () => ({ storage: storage.value, path: path.value.trim() }));
    }
    function editRule(panel, user, entry, presets) {
      if (!ready(panel)) return;
      open(entry ? "editRule" : "addRule");
      const type = field("type", "ruleMatch", entry?.type || "FILENAME-REGEX", Object.entries(ruleTypes).map(([value, key]) => [value, t(key)])).input;
      const preset = field("preset", "ruleFilePreset", entry ? presets.find((p) => p.data === entry.data)?.name || "" : presets[0]?.name || "",
        [["", t("ruleCustom")], ...presets.map((p) => [p.name, t(`category_${p.name}`)])]);
      const data = field("data", "ruleExpression", entry?.data || "");
      data.input.placeholder = t("ruleExpressionExample");
      const album = field("album", "ruleAlbumChoice", entry?.data === "false" ? "false" : "true", [["true", t("ruleIsAlbum")], ["false", t("ruleSingleMedia")]]);
      const hint = node("p", t("ruleExpressionNote"), "small muted"); fields.append(hint);
      function matchFields() {
        const file = type.value === "FILENAME-REGEX", grouped = type.value === "IS-ALBUM";
        preset.wrap.hidden = !file; album.wrap.hidden = !grouped;
        data.wrap.hidden = grouped || file && !!preset.input.value; data.input.required = !data.wrap.hidden;
        hint.hidden = data.wrap.hidden;
        if (file && preset.input.value) data.input.value = presets.find((p) => p.name === preset.input.value).data;
      }
      type.addEventListener("change", matchFields); preset.input.addEventListener("change", matchFields); matchFields();
      const storage = field("storage", "ruleDestination", entry?.storage || "CHOSEN", [["CHOSEN", t("ruleChosenStorage")], ...storageChoices()]).input;
      const path = field("path", "directoryPath", entry?.path || "").input;
      path.placeholder = t("rulePathExample");
      fields.append(node("p", t("rulePathNote"), "small muted"), node("p", t("ruleEnableNote"), "note"));
      editing(user, "rules", entry, () => {
        const values = { type: type.value, storage: storage.value, path: path.value.trim() };
        const expression = type.value === "IS-ALBUM" ? album.input.value : data.input.value;
        if (!entry || type.value !== entry.type || expression !== entry.data) values.data = expression;
        return values;
      });
    }

    function actions(panel, user, endpoint, entry, edit) {
      const group = node("div", null, "row-actions");
      const remove = button(t("deleteSetting"), async () => {
        if (!ready(panel)) return;
        const note = endpoint === "directories" ? t("deleteDirectoryNote") : t("deleteRuleNote");
        const title = endpoint === "directories" ? `${entry.storage}: ${entry.path}` : `${t(ruleTypes[entry.type])}: ${entry.data}`;
        if (!confirm(`${note}\n\n${title}`)) return;
        remove.disabled = true;
        try {
          await request(`${endpoint}/${entry.id}`, { method: "DELETE", body: JSON.stringify({ user_chat_id: user.chat_id }) });
          toast(t("deletedSetting")); await reload();
        } catch (failure) { toast(failure.message); }
        finally { remove.disabled = false; }
      }, "secondary small-button danger-text");
      group.append(button(t("editSetting"), edit), remove); return group;
    }
    function render(panel, user, presets) {
      const dirs = node("section", null, "settings-section"), dirHeading = node("div", null, "panel-heading");
      dirHeading.append(node("h2", t("directories")), button(t("addDirectory"), () => editDirectory(panel, user)));
      dirs.append(dirHeading, node("p", t("directoryListNote"), "small muted"));
      const dirTable = node("div");
      table(dirTable, ["storage", "path", "actions"], user.directories.map((entry) => [entry.storage,
        `${entry.path}${entry.id === user.default_dir ? ` · ${t("currentDefault")}` : ""}`,
        actions(panel, user, "directories", entry, () => editDirectory(panel, user, entry))]));
      dirs.append(dirTable); panel.append(dirs);
      const rules = node("section", null, "settings-section"), ruleHeading = node("div", null, "panel-heading");
      ruleHeading.append(node("h2", t("rules")), button(t("addRule"), () => editRule(panel, user, null, presets)));
      rules.append(ruleHeading, node("p", t(user.apply_rule ? "rulesEnabledNote" : "rulesDisabledNote"), "small muted"));
      const ruleTable = node("div");
      table(ruleTable, ["ruleType", "ruleData", "storage", "path", "actions"], user.rules.map((entry) => {
        const preset = entry.type === "FILENAME-REGEX" && presets.find((p) => p.data === entry.data);
        return [t(ruleTypes[entry.type]) || entry.type, entry.type === "IS-ALBUM" ? t(entry.data === "true" ? "ruleIsAlbum" : "ruleSingleMedia") : preset ? t(`category_${preset.name}`) : entry.data,
          entry.storage === "CHOSEN" ? t("ruleChosenStorage") : entry.storage,
          entry.path === "NEW-FOR-ALBUM" ? t("captionDirectory") : entry.path || t("storageRoot"),
          actions(panel, user, "rules", entry, () => editRule(panel, user, entry, presets))];
      }));
      rules.append(ruleTable); panel.append(rules);
    }
    return { render, close };
  }
  globalThis.SaveAnyPreferences = { init };
})();
