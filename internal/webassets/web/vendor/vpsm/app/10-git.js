// 10-git.js — VPS Manager’s visual Git client.
//
// A rich reimplementation inspired by Git Graph (mhutchie): a graph with
// avatars, tags, stashes and remote branches; context menus (commit/branch/tag/
// stash); action dialogs (branch, tag, merge, rebase, reset, cherry-pick,
// revert, stash, PR); fetch/pull/push; search; and an IDENTITY/ACCOUNT PICKER
// for the commit author.
//
// Coupling: window.VPSMGitModule() is spread into the app() component (see
// 00-shell.js), so `this` is the component — this.api/showToast/_ensureMonaco
// /_langForPath/$nextTick work natively. The graph LAYOUT (lanes/edges) comes
// from the server; here we only draw it.
(function () {
  'use strict';

  var ROWH = 30, LANEW = 16, PADX = 14, DOTR = 4.5;
  var LANE_COLORS = ['#60a5fa', '#f59e0b', '#34d399', '#f472b6', '#a78bfa', '#22d3ee', '#fb7185', '#a3e635', '#e879f9', '#2dd4bf'];
  // selectable graph palettes
  var PALETTES = {
    vivid: LANE_COLORS,
    github: ['#58a6ff', '#3fb950', '#a371f7', '#f85149', '#d29922', '#79c0ff', '#56d364', '#bc8cff', '#ff7b72', '#e3b341'],
    pastel: ['#9db4ff', '#ffd29b', '#a7e8c4', '#f7b7d2', '#c9b8f0', '#a8e6ef', '#f6b8bf', '#cfe89b', '#f0c0f0', '#9fe6db'],
    mono: ['#8b949e', '#adbac7', '#768390', '#cdd9e5', '#636e7b', '#909dab', '#7a8694', '#bcc7d1', '#5b6570', '#9aa5b1'],
  };
  var AVA_COLORS = ['#ef4444', '#f59e0b', '#10b981', '#3b82f6', '#8b5cf6', '#ec4899', '#14b8a6', '#f97316'];

  // simple deterministic hash (djb2) for a per-e-mail avatar colour.
  function hashStr(s) {
    var h = 5381;
    for (var i = 0; i < (s || '').length; i++) h = ((h << 5) + h + s.charCodeAt(i)) >>> 0;
    return h;
  }
  function initials(name) {
    var p = (name || '?').trim().split(/\s+/);
    if (p.length === 1) return (p[0][0] || '?').toUpperCase();
    return (p[0][0] + p[p.length - 1][0]).toUpperCase();
  }

  // Official GitHub Octicons (16px, paths verbatim from @primer/octicons).
  var OCTICONS = {
    'git-pull-request': 'M1.5 3.25a2.25 2.25 0 1 1 3 2.122v5.256a2.251 2.251 0 1 1-1.5 0V5.372A2.25 2.25 0 0 1 1.5 3.25Zm5.677-.177L9.573.677A.25.25 0 0 1 10 .854V2.5h1A2.5 2.5 0 0 1 13.5 5v5.628a2.251 2.251 0 1 1-1.5 0V5a1 1 0 0 0-1-1h-1v1.646a.25.25 0 0 1-.427.177L7.177 3.427a.25.25 0 0 1 0-.354ZM3.75 2.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm0 9.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm8.25.75a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0Z',
    'git-merge': 'M5.45 5.154A4.25 4.25 0 0 0 9.25 7.5h1.378a2.251 2.251 0 1 1 0 1.5H9.25A5.734 5.734 0 0 1 5 7.123v3.505a2.25 2.25 0 1 1-1.5 0V5.372a2.25 2.25 0 1 1 1.95-.218ZM4.25 13.5a.75.75 0 1 0 0-1.5.75.75 0 0 0 0 1.5Zm8.5-4.5a.75.75 0 1 0 0-1.5.75.75 0 0 0 0 1.5ZM5 3.25a.75.75 0 1 0-1.5 0 .75.75 0 0 0 1.5 0Z',
    'git-pull-request-closed': 'M3.25 1A2.25 2.25 0 0 1 4 5.372v5.256a2.251 2.251 0 1 1-1.5 0V5.372A2.251 2.251 0 0 1 3.25 1Zm9.5 5.5a.75.75 0 0 1 .75.75v3.378a2.251 2.251 0 1 1-1.5 0V7.25a.75.75 0 0 1 .75-.75Zm-2.03-5.273a.75.75 0 0 1 1.06 0l.97.97.97-.97a.748.748 0 0 1 1.265.332.75.75 0 0 1-.205.729l-.97.97.97.97a.751.751 0 0 1-.018 1.042.751.751 0 0 1-1.042.018l-.97-.97-.97.97a.749.749 0 0 1-1.275-.326.749.749 0 0 1 .215-.734l.97-.97-.97-.97a.75.75 0 0 1 0-1.06ZM2.5 3.25a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0ZM3.25 12a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm9.5 0a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Z',
    'git-pull-request-draft': 'M3.25 1A2.25 2.25 0 0 1 4 5.372v5.256a2.251 2.251 0 1 1-1.5 0V5.372A2.251 2.251 0 0 1 3.25 1Zm9.5 14a2.25 2.25 0 1 1 0-4.5 2.25 2.25 0 0 1 0 4.5ZM2.5 3.25a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0ZM3.25 12a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm9.5 0a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5ZM14 7.5a1.25 1.25 0 1 1-2.5 0 1.25 1.25 0 0 1 2.5 0Zm0-4.25a1.25 1.25 0 1 1-2.5 0 1.25 1.25 0 0 1 2.5 0Z',
    'check': 'M13.78 4.22a.75.75 0 0 1 0 1.06l-7.25 7.25a.75.75 0 0 1-1.06 0L2.22 9.28a.751.751 0 0 1 .018-1.042.751.751 0 0 1 1.042-.018L6 10.94l6.72-6.72a.75.75 0 0 1 1.06 0Z',
    'x': 'M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z',
    'dot-fill': 'M8 4a4 4 0 1 1 0 8 4 4 0 0 1 0-8Z',
    'git-commit': 'M11.93 8.5a4.002 4.002 0 0 1-7.86 0H.75a.75.75 0 0 1 0-1.5h3.32a4.002 4.002 0 0 1 7.86 0h3.32a.75.75 0 0 1 0 1.5Zm-1.43-.75a2.5 2.5 0 1 0-5 0 2.5 2.5 0 0 0 5 0Z',
    'git-branch': 'M9.5 3.25a2.25 2.25 0 1 1 3 2.122V6A2.5 2.5 0 0 1 10 8.5H6a1 1 0 0 0-1 1v1.128a2.251 2.251 0 1 1-1.5 0V5.372a2.25 2.25 0 1 1 1.5 0v1.836A2.493 2.493 0 0 1 6 7h4a1 1 0 0 0 1-1v-.628A2.25 2.25 0 0 1 9.5 3.25Zm-6 0a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0Zm8.25-.75a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5ZM4.25 12a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Z',
    'tag': 'M1 7.775V2.75C1 1.784 1.784 1 2.75 1h5.025c.464 0 .91.184 1.238.513l6.25 6.25a1.75 1.75 0 0 1 0 2.474l-5.026 5.026a1.75 1.75 0 0 1-2.474 0l-6.25-6.25A1.752 1.752 0 0 1 1 7.775Zm1.5 0c0 .066.026.13.073.177l6.25 6.25a.25.25 0 0 0 .354 0l5.025-5.025a.25.25 0 0 0 0-.354l-6.25-6.25a.25.25 0 0 0-.177-.073H2.75a.25.25 0 0 0-.25.25ZM6 5a1 1 0 1 1 0 2 1 1 0 0 1 0-2Z',
    'file': 'M2 1.75C2 .784 2.784 0 3.75 0h6.586c.464 0 .909.184 1.237.513l2.914 2.914c.329.328.513.773.513 1.237v9.586A1.75 1.75 0 0 1 13.25 16h-9.5A1.75 1.75 0 0 1 2 14.25Zm1.75-.25a.25.25 0 0 0-.25.25v12.5c0 .138.112.25.25.25h9.5a.25.25 0 0 0 .25-.25V6h-2.75A1.75 1.75 0 0 1 9 4.25V1.5Zm6.75.062V4.25c0 .138.112.25.25.25h2.688l-.011-.013-2.914-2.914-.013-.011Z',
    'file-directory-fill': 'M1.75 1A1.75 1.75 0 0 0 0 2.75v10.5C0 14.216.784 15 1.75 15h12.5A1.75 1.75 0 0 0 16 13.25v-8.5A1.75 1.75 0 0 0 14.25 3H7.5a.25.25 0 0 1-.2-.1l-.9-1.2C6.07 1.26 5.55 1 5 1H1.75Z',
  };
  function octicon(name, size) {
    var p = OCTICONS[name]; if (!p) return '';
    var s = size || 14;
    return '<svg class="gh-oct" viewBox="0 0 16 16" width="' + s + '" height="' + s + '" fill="currentColor" aria-hidden="true"><path d="' + p + '"/></svg>';
  }

  window.VPSMGitModule = function () {
    return {
      git: {
        repos: [], repo: '', repoInfo: null,
        loading: false, busy: false, error: '',
        graph: null, status: null, branches: [], tags: [], stashes: [], remotes: [], identities: [],
        identityId: '',                 // account/author chosen for commits
        selected: '', commitDetail: null,
        view: 'changes',                // 'commit' | 'changes' | 'branches' | 'tags' | 'stashes'
        commitMessage: '', newBranch: '',
        commitAmend: false, commitSignoff: false, commitCoauthors: '',
        commitType: '', commitScope: '',   // conventional-commits template
        worktrees: [], worktreeForm: { name: '', branch: '', create_branch: true, base: '' },
        submodules: [], branchFilter: '', branchFavs: [],
        identityWarn: false,
        find: '', findOpen: false, findHits: [], findIdx: 0,
        filter: { scope: 'all', author: '', since: '', branch: '' }, // graph: ref scope + author/date
        filterOpen: false, graphTruncated: false,
        prs: [], prDetail: null, prFiles: [], prComments: [], prReviews: [], prCommits: [], prChecks: null,
        prReviewComments: [], prFileOpen: '', reviewedSet: [],
        prView: 'list', prTab: 'conv', prLoading: false, prErr: '', prInfo: null,
        prState: 'open', prComment: '', prMergeMethod: 'merge', prBusy: false, prDeleteBranch: false, prReviewBody: '',
        ctx: { open: false, x: 0, y: 0, type: '', data: null },
        dialog: { open: false, kind: '', title: '', target: null, name: '', message: '', ref: '', mode: 'mixed', flag1: false, flag2: false, danger: false, confirmLabel: 'Confirm', body: '' },
        editor: { open: false, mode: 'edit', path: '', saving: false, _inst: null, _model: null },
        // inspection panel (overlay): per-line blame, file history, compare refs
        inspect: { open: false, kind: '', loading: false, err: '', path: '', ref: '', blame: [], filelog: [], compare: null, a: '', b: '', mode: 'threedot', hunks: [], reflog: [], conflicts: null, rebaseBase: '', rebaseTodo: [], rebaseBusy: false, stashDiff: '', title: '' },
        blameLimit: 300, filelogLimit: 200, hunksLimit: 200, reflogLimit: 200, cmpFilesLimit: 200, cmpCommitsLimit: 200, // cap + show-more on the large git lists
        dragCommit: '',                 // hash being dragged (drag-and-drop)
        layout: 'cols',                 // 'cols' (side by side, default) | 'rows' (stacked)
        cols: { graph: 0, author: 150, date: 80, hash: 84 }, // history column widths (px) — resizable (graph = px BEYOND the lanes)
        _colDrag: null,
        settings: {
          editorTheme: 'vs-dark', fontSize: 13, wordWrap: 'off', minimap: false,
          density: 'comfortable',       // 'compact' | 'comfortable' | 'spacious'
          showAvatars: true, dateFormat: 'rel', limit: 150,
          inlineBlame: true,            // inline blame in the editor (GitLens-style)
          diffSideBySide: true,         // side-by-side diff (vs inline)
          autoFetch: false, autoFetchMin: 5, // periodic remote fetch (incoming/outgoing)
          graphTheme: 'dark', graphPalette: 'vivid', // graph appearance
        },
      },

      // ===== freeze debug instrumentation =====
      // [GITDBG] in the console: every step of opening the editor plus a
      // long-task observer that catches ANY main-thread block (the last line
      // logged before the freeze points at the culprit).
      gitlog() { try { console.log.apply(console, ['[GITDBG]'].concat([].slice.call(arguments))); } catch (e) {} },
      _gitInstrument() {
        if (this._gitDbgOn) return; this._gitDbgOn = true;
        try {
          const po = new PerformanceObserver((list) => {
            list.getEntries().forEach((e) => { if (e.duration > 80) console.warn('[GITDBG] LONG-TASK ' + Math.round(e.duration) + 'ms', e.name || ''); });
          });
          po.observe({ entryTypes: ['longtask'] });
          this.gitlog('long-task observer ON');
        } catch (e) { this.gitlog('longtask observer falhou', e.message); }
        window.addEventListener('error', (ev) => { try { console.error('[GITDBG] window.error', ev.message, ev.filename + ':' + ev.lineno); } catch (e) {} });
      },

      // ===== lifecycle =====
      async gitInit() {
        this._gitInstrument();
        this.gitLoadSettings();
        await this.gitLoadLayoutProfile();
        if (this.git.repos.length === 0) await this.gitLoadRepos();
        if (this.git.identities.length === 0) await this.gitLoadIdentities();
        if (this.git.repo) await this.gitLoad();
        this._gitAutoFetchWire();
      },
      // periodic auto-fetch (opt-in): it checks every minute, but only really
      // fetches every autoFetchMin minutes and only on the git page — it refreshes
      // incoming/outgoing.
      _gitAutoFetchWire() {
        if (this._gitAutoFetchTimer) return;
        const self = this; this._gitAutoFetchLast = 0;
        this._gitAutoFetchTimer = setInterval(function () {
          if (document.hidden) return;
          if (!self.git.settings.autoFetch || self.currentView !== 'git' || !self.git.repo) return;
          const now = Date.now(), minMs = (self.git.settings.autoFetchMin || 5) * 60000;
          if (self._gitAutoFetchLast && (now - self._gitAutoFetchLast) < minMs) return;
          self._gitAutoFetchLast = now; self._gitSilentFetch();
        }, 60000);
      },
      async _gitSilentFetch() {
        try {
          await this.api('/api/git/fetch?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({}) });
          await this.gitLoadRepos();
          this.git.repoInfo = this.gitCurrentRepo();
        } catch (e) {}
      },
      gitLoadSettings() {
        try { const s = JSON.parse(localStorage.getItem('vpsm_git_settings') || '{}'); Object.assign(this.git.settings, s); } catch (e) {}
        try { const l = localStorage.getItem('vpsm_git_layout'); if (l) this.git.layout = l; } catch (e) {}
      },
      gitSaveSettings() {
        try { localStorage.setItem('vpsm_git_settings', JSON.stringify(this.git.settings)); } catch (e) {}
      },
      gitApplySettings() {
        this.gitSaveSettings();
        // applies theme/font/wrap/minimap to the iframe editor (if it is open)
        const w = this._gitFrameWin();
        if (this.git.editor.open && w && typeof w.gitEditorUpdateOptions === 'function') {
          try { w.gitEditorUpdateOptions({ theme: this.git.settings.editorTheme, fontSize: this.git.settings.fontSize, wordWrap: this.git.settings.wordWrap, minimap: this.git.settings.minimap }); } catch (e) {}
        }
      },
      gitToggleLayout() { this.git.layout = this.git.layout === 'rows' ? 'cols' : 'rows'; try { localStorage.setItem('vpsm_git_layout', this.git.layout); } catch (e) {} },

      // ===== resizable history columns =====
      // effective width of the graph column (lanes + the user’s adjustment),
      // never negative — it can go to 0 to HIDE the graph completely.
      gitGraphColW() { return Math.max(0, this.gitGraphWidth() + (this.git.cols.graph || 0)); },
      // grid-template-columns: Graph | Message(flex) | Author | Date | Hash
      gitGridTemplate() {
        const c = this.git.cols;
        return this.gitGraphColW() + 'px minmax(80px,1fr) ' + (c.author || 150) + 'px ' + (c.date || 80) + 'px ' + (c.hash || 84) + 'px';
      },
      gitColStart(col, sign, ev) {
        ev.preventDefault(); ev.stopPropagation();
        const sx = ev.clientX, sw = this.git.cols[col] || 0, self = this;
        // the graph can go well negative (shrinking until it hides); the effective
        // width is clamped to >=0 in gitGraphColW. Other columns: minimum 0.
        const min = col === 'graph' ? -5000 : 0;
        const mv = function (e) { self.git.cols[col] = Math.max(min, Math.min(900, sw + sign * (e.clientX - sx))); };
        const up = function () { document.removeEventListener('mousemove', mv); document.removeEventListener('mouseup', up); document.body.style.cursor = ''; };
        document.addEventListener('mousemove', mv); document.addEventListener('mouseup', up); document.body.style.cursor = 'col-resize';
      },

      // ===== PER-PROJECT layout in the PROFILE (server-side, /api/user/prefs) =====
      // Stored shape: { settings:{...global editor}, repos:{ <repoId>:{cols,layout} } }
      _gitProfile: { settings: {}, repos: {} },
      _gitDefaultCols() { return { graph: 0, author: 150, date: 80, hash: 84 }; },
      async gitLoadLayoutProfile() {
        try {
          // Best-effort: with no stored profile the layout falls back to the default —
          // api() throws and the empty catch at the end absorbs it.
          const r = await this.api('/api/user/prefs?key=git');
          const d = await r.json().catch(() => ({}));
          const v = d && (d.value || d);
          if (v && typeof v === 'object') {
            this._gitProfile = { settings: v.settings || {}, repos: v.repos || {} };
            if (v.settings) Object.assign(this.git.settings, v.settings); // the editor is global
          }
        } catch (e) {}
      },
      // applies the stored layout OF the current REPO (or back to the default)
      gitApplyRepoLayout(repoId) {
        const r = (this._gitProfile.repos || {})[repoId];
        if (r && typeof r === 'object') {
          this.git.cols = Object.assign(this._gitDefaultCols(), r.cols || {});
          if (r.layout) this.git.layout = r.layout;
        } else {
          this.git.cols = this._gitDefaultCols();
        }
      },
      async gitSaveLayoutProfile() {
        if (!this._gitProfile.repos) this._gitProfile.repos = {};
        this._gitProfile.repos[this.git.repo] = { cols: Object.assign({}, this.git.cols), layout: this.git.layout };
        this._gitProfile.settings = Object.assign({}, this.git.settings);
        try {
          await this.api('/api/user/prefs', { method: 'POST', body: JSON.stringify({ key: 'git', value: this._gitProfile }) });
          this.gitSaveSettings();
          this.showToast('project layout saved to your profile ✓', 'ok');
        } catch (e) { this.showToast('could not save layout: ' + this._errText(e), 'err'); }
      },
      // graph row height by density (px) — the SVG and the rows both use this
      rowH() { const d = this.git.settings.density; return d === 'compact' ? 24 : (d === 'spacious' ? 36 : 30); },
      // date according to the preference (relative/absolute)
      gitDate(iso) { return this.git.settings.dateFormat === 'abs' ? this.gitFmtDate(iso) : this.gitRelDate(iso); },
      async gitLoadRepos() {
        this.git.error = '';
        try {
          const r = await this.api('/api/git/repos');
          const d = await r.json();
          this.git.repos = (d && d.repos) || [];
          this.git.identityWarn = this.git.repos.some(function (x) { return x.writable && !x.identity_ok; });
          if (!this.git.repo && this.git.repos.length) this.git.repo = this.git.repos[0].id;
        } catch (e) { this.git.error = 'could not list repositories: ' + this._errText(e); }
      },
      async gitLoadIdentities() {
        try {
          const r = await this.api('/api/git/identities');
          const d = await r.json();
          this.git.identities = (d && d.identities) || [];
        } catch (e) {}
      },
      gitCurrentRepo() { const id = this.git.repo; return this.git.repos.find(function (r) { return r.id === id; }) || null; },

      async gitLoad() {
        if (!this.git.repo) return;
        this.git.loading = true; this.git.error = '';
        this.git.selected = ''; this.git.commitDetail = null;
        this.gitCloseEditor(); this.gitCloseCtx();
        this.git.repoInfo = this.gitCurrentRepo();
        this.gitApplyRepoLayout(this.git.repo); // this project’s stored layout
        this.gitLoadFavs();
        // default identity = the one the repo expects (matched by e-mail), else the 1st
        this.gitPickDefaultIdentity();
        try {
          await Promise.all([
            this.gitLoadGraph(), this.gitLoadStatus(), this.gitLoadBranches(),
            this.gitLoadTags(), this.gitLoadStashes(), this.gitLoadRemotes(),
            this.gitLoadWorktrees(), this.gitLoadSubmodules(),
          ]);
        } catch (e) { this.git.error = e.message; }
        finally { this.git.loading = false; }
      },
      gitPickDefaultIdentity() {
        const ri = this.git.repoInfo;
        if (ri && ri.expected_identity) {
          const m = this.git.identities.find(i => ri.expected_identity.indexOf(i.email) >= 0);
          if (m) { this.git.identityId = m.id; return; }
        }
        if (!this.git.identityId && this.git.identities.length) this.git.identityId = this.git.identities[0].id;
      },
      gitSelectedIdentity() { const id = this.git.identityId; return this.git.identities.find(i => i.id === id) || null; },

      async gitLoadGraph() {
        const f = this.git.filter || {};
        let u = '/api/git/graph?repo=' + encodeURIComponent(this.git.repo) + '&limit=' + (this.git.settings.limit || 150);
        if (f.scope === 'current') u += '&current=1';
        else if (f.scope === 'no_remotes') u += '&no_remotes=1';
        else if (f.scope === 'branch' && f.branch) u += '&branch=' + encodeURIComponent(f.branch);
        if (f.author) u += '&author=' + encodeURIComponent(f.author);
        if (f.since) u += '&since=' + encodeURIComponent(f.since);
        // its own try/catch: gitApplyFilter()/gitLoadMore() call it without await
        // and without catch, so the failure has to become git.error here instead
        // of escaping.
        try {
          const r = await this.api(u);
          this.git.graph = await r.json();
          this.git.graphTruncated = !!(this.git.graph && this.git.graph.truncated);
          this.gitDecorateGraph();
        } catch (e) { this.git.error = 'graph failed: ' + this._errText(e); this.git.graph = null; }
      },
      // applies the filters (resets limit to the default) and reloads
      gitApplyFilter() { this.git.settings.limit = 150; this.gitLoadGraph(); },
      gitClearFilter() { this.git.filter = { scope: 'all', author: '', since: '', branch: '' }; this.gitApplyFilter(); },
      gitFilterActive() { const f = this.git.filter || {}; return f.scope !== 'all' || !!f.author || !!f.since; },
      // "load more": raises the limit and reloads (keeps the filters)
      async gitLoadMore() { this.git.settings.limit = Math.min(2000, (this.git.settings.limit || 150) + 250); await this.gitLoadGraph(); },
      // Precomputes per commit (avatar/badges/dates) ONCE per load, instead of
      // calling functions in the bindings of each of the N rows on every render —
      // this keeps the graph light even with many commits.
      gitDecorateGraph() {
        const g = this.git.graph; if (!g || !g.commits) return;
        const self = this;
        g.commits.forEach(function (c) {
          c._ava = self.gitAvatar(c.author, c.email);
          c._badges = (c.refs || []).map(function (r) { return self.gitRefBadge(r); });
          c._date = self.gitDate(c.date);
          c._dateAbs = self.gitFmtDate(c.date);
        });
      },
      // Side panels: each one absorbs its own failure (console warn, the data stays
      // as it was). They are called both inside gitLoad()’s Promise.all and on
      // their own after each action (gitStage, gitCommit, …) — those standalone
      // call-sites have no catch, so throwing here would become an unhandled
      // rejection. The Git panel’s visible error comes from gitLoadGraph().
      async gitLoadStatus() { try { const r = await this.api('/api/git/status?repo=' + encodeURIComponent(this.git.repo)); this.git.status = await r.json(); } catch (e) { console.warn('[git] status', e); } },
      async gitLoadBranches() { try { const r = await this.api('/api/git/branches?repo=' + encodeURIComponent(this.git.repo)); const d = await r.json(); this.git.branches = (d && d.branches) || []; } catch (e) { console.warn('[git] branches', e); } },
      async gitLoadTags() { try { const r = await this.api('/api/git/tags?repo=' + encodeURIComponent(this.git.repo)); const d = await r.json(); this.git.tags = (d && d.tags) || []; } catch (e) { console.warn('[git] tags', e); } },
      async gitLoadStashes() { try { const r = await this.api('/api/git/stashes?repo=' + encodeURIComponent(this.git.repo)); const d = await r.json(); this.git.stashes = (d && d.stashes) || []; } catch (e) { console.warn('[git] stashes', e); } },
      async gitLoadRemotes() { try { const r = await this.api('/api/git/remotes?repo=' + encodeURIComponent(this.git.repo)); const d = await r.json(); this.git.remotes = (d && d.remotes) || []; } catch (e) { console.warn('[git] remotes', e); } },

      // ===== avatar (initials, no gravatar/tracking) =====
      gitAvatar(name, email) {
        return { text: initials(name), color: AVA_COLORS[hashStr(email || name) % AVA_COLORS.length] };
      },

      // ===== icon by file/folder type =====
      gitIsFolder(path) { return !!path && path.charAt(path.length - 1) === '/'; },
      gitFileIcon(path) {
        if (!path) return '📄';
        if (this.gitIsFolder(path)) return '📁';
        const base = path.split('/').pop().toLowerCase();
        if (base.indexOf('.') < 0) return '📄';
        const ext = base.split('.').pop();
        const M = {
          pdf: '📕',
          png: '🖼️', jpg: '🖼️', jpeg: '🖼️', gif: '🖼️', svg: '🖼️', webp: '🖼️', ico: '🖼️', bmp: '🖼️',
          zip: '📦', tar: '📦', gz: '📦', tgz: '📦', '7z': '📦', rar: '📦', xz: '📦',
          exe: '⚙️', bin: '⚙️', so: '⚙️', o: '⚙️', a: '⚙️', dll: '⚙️', dylib: '⚙️', wasm: '⚙️',
          mp4: '🎬', mov: '🎬', mkv: '🎬', webm: '🎬', mp3: '🎵', wav: '🎵', ogg: '🎵', flac: '🎵',
          md: '📝', txt: '📄', log: '📄', csv: '📊', xlsx: '📊',
          go: '🐹', js: '📜', mjs: '📜', ts: '📜', tsx: '📜', jsx: '📜', py: '🐍', rs: '🦀', rb: '💎',
          java: '☕', kt: '📜', c: '📜', h: '📜', cpp: '📜', cc: '📜', hpp: '📜', sh: '📜', bash: '📜', php: '📜',
          json: '🔧', yaml: '🔧', yml: '🔧', toml: '🔧', ini: '🔧', conf: '🔧', cfg: '🔧', env: '🔧',
          html: '🌐', htm: '🌐', css: '🎨', scss: '🎨', sql: '🗄️',
        };
        return M[ext] || '📄';
      },

      // ===== SVG graph (rowH dynamic by density) =====
      gitGraphWidth() { const ml = this.git.graph ? (this.git.graph.max_lane || 0) : 0; return PADX * 2 + (ml + 1) * LANEW; },
      gitGraphSVG() {
        const g = this.git.graph;
        if (!g || !g.commits || !g.commits.length) return '';
        const RH = this.rowH();
        const W = this.gitGraphWidth(), H = g.commits.length * RH;
        const PAL = this.gitLaneColors();
        const dotStroke = this.git.settings.graphTheme === 'light' ? '#fff' : '#0f1117';
        let paths = '';
        (g.edges || []).forEach(function (e) {
          const x1 = PADX + e.from_lane * LANEW, y1 = e.from_row * RH + RH / 2;
          const color = PAL[e.to_lane % PAL.length];
          if (e.dangling) {
            const xt = PADX + e.to_lane * LANEW;
            paths += '<path d="M' + x1 + ' ' + y1 + ' L' + xt + ' ' + (y1 + RH) + ' L' + xt + ' ' + H + '" stroke="' + color + '" stroke-width="2" fill="none" opacity="0.35" stroke-dasharray="2 3"/>';
          } else {
            const x2 = PADX + e.to_lane * LANEW, y2 = e.to_row * RH + RH / 2;
            if (x1 === x2) paths += '<path d="M' + x1 + ' ' + y1 + ' L' + x2 + ' ' + y2 + '" stroke="' + color + '" stroke-width="2" fill="none"/>';
            else { const my = (y1 + y2) / 2; paths += '<path d="M' + x1 + ' ' + y1 + ' C ' + x1 + ' ' + my + ' ' + x2 + ' ' + my + ' ' + x2 + ' ' + y2 + '" stroke="' + color + '" stroke-width="2" fill="none"/>'; }
          }
        });
        let dots = '';
        for (let i = 0; i < g.commits.length; i++) {
          const c = g.commits[i], cx = PADX + c.lane * LANEW, cy = i * RH + RH / 2;
          const color = PAL[c.lane % PAL.length];
          const isHead = (c.refs || []).some(function (r) { return r.indexOf('HEAD') >= 0; });
          dots += '<circle cx="' + cx + '" cy="' + cy + '" r="' + (isHead ? DOTR + 1.5 : DOTR) + '" fill="' + color + '" stroke="' + (isHead ? '#fff' : dotStroke) + '" stroke-width="' + (isHead ? 2 : 1.5) + '"/>';
        }
        return '<svg width="' + W + '" height="' + H + '" viewBox="0 0 ' + W + ' ' + H + '">' + paths + dots + '</svg>';
      },

      // classifies a ref from %D into {kind,label}
      gitRefBadge(ref) {
        ref = ref.replace('HEAD -> ', '');
        if (ref === 'HEAD') return { kind: 'head', label: 'HEAD' };
        if (ref.indexOf('tag: ') === 0) return { kind: 'tag', label: ref.slice(5) };
        if (ref.indexOf('origin/') === 0 || ref.indexOf('/') >= 0 && ref.split('/')[0].match(/^(origin|upstream)/)) return { kind: 'remote', label: ref };
        return { kind: 'local', label: ref };
      },

      // ===== selected commit =====
      async gitSelectCommit(hash) {
        this.git.selected = hash; this.git.view = 'commit';
        this.gitLoadReviewed('c:' + this.git.repo + ':' + hash);
        try { const r = await this.api('/api/git/show?repo=' + encodeURIComponent(this.git.repo) + '&hash=' + encodeURIComponent(hash)); this.git.commitDetail = await r.json(); }
        catch (e) { this.showToast('could not load the commit: ' + this._errText(e), 'err'); }
      },
      // Diff of one file from the selected commit: side by side (parent vs commit)
      // in the Monaco DiffEditor. Falls back to a textual unified diff when the
      // two sides cannot be assembled (e.g. binary).
      async gitViewCommitDiff(path) {
        const hash = this.git.selected;
        const parents = (this.git.commitDetail && this.git.commitDetail.parents) || [];
        const parent = parents[0] || '';
        const mod = await this._gitFileAt(path, hash);
        const orig = parent ? await this._gitFileAt(path, parent) : '';
        if (mod === null && (orig === null || orig === '')) {
          return this._gitShowDiff(path, '/api/git/diff?repo=' + encodeURIComponent(this.git.repo) + '&hash=' + encodeURIComponent(hash) + '&path=' + encodeURIComponent(path));
        }
        this._gitOpenDiffEditor(path, orig || '', mod || '');
      },
      // Working-tree diff (HEAD vs disk) in the DiffEditor.
      async gitDiffWorking(path) {
        if (this.gitIsFolder(path)) return;
        const mod = await this._gitFileAt(path, '');       // working tree
        const orig = await this._gitFileAt(path, 'HEAD');   // the version in HEAD
        if (mod === null && (orig === null || orig === '')) { this.showToast('diff unavailable (binary?)', 'err'); return; }
        this._gitOpenDiffEditor(path, orig || '', mod || '');
      },
      // reads a file’s content at a ref (''=working tree). null on error/binary.
      async _gitFileAt(path, ref) {
        try {
          let u = '/api/git/file?repo=' + encodeURIComponent(this.git.repo) + '&path=' + encodeURIComponent(path);
          if (ref) u += '&ref=' + encodeURIComponent(ref);
          // The helper’s contract is "null on error" — api() throws and the catch
          // returns null; the call-sites already handle null (falling back to the
          // unified diff).
          const r = await this.api(u);
          const d = await r.json().catch(() => ({}));
          return d.content || '';
        } catch (e) { return null; }
      },
      _gitOpenDiffEditor(path, original, modified) {
        this.git.editor.open = true; this.git.editor.mode = 'diff'; this.git.editor.path = path;
        this.$nextTick(() => this._gitFrameOpenDiff({ original: original, modified: modified, language: this._langForPath(path), sideBySide: this.git.settings.diffSideBySide !== false }));
      },
      gitToggleDiffLayout() {
        this.git.settings.diffSideBySide = !this.git.settings.diffSideBySide;
        this.gitSaveSettings();
        const w = this._gitFrameWin();
        if (w && typeof w.gitEditorSetDiffLayout === 'function') { try { w.gitEditorSetDiffLayout(this.git.settings.diffSideBySide); } catch (e) {} }
      },

      // ===== editor / diff (Monaco runs in the IFRAME #git-monaco-frame) =====
      // The editor lives in an iframe (a separate document) → Alpine’s
      // MutationObserver does NOT watch Monaco’s DOM, which eliminates the
      // freeze. The parent merely calls the iframe’s window.gitEditor*
      // (same-origin).
      _gitFrameWin() { const f = document.getElementById('git-monaco-frame'); return f ? f.contentWindow : null; },
      // Waits for the iframe to have Monaco ready (gitEditorReady) and then calls
      // gitEditorOpen. Short poll; resolves true/false.
      _gitFrameOpen(opts) {
        const self = this;
        return new Promise(function (resolve) {
          let tries = 0;
          (function attempt() {
            const w = self._gitFrameWin();
            if (w && typeof w.gitEditorReady === 'function' && w.gitEditorReady()) {
              self.gitlog('iframe pronto; gitEditorOpen lang=' + (opts.language || '?'));
              const ok = w.gitEditorOpen(Object.assign({
                theme: self.git.settings.editorTheme, fontSize: self.git.settings.fontSize,
                minimap: self.git.settings.minimap, wordWrap: self.git.settings.wordWrap,
              }, opts));
              self.gitlog('gitEditorOpen retornou', ok);
              resolve(!!ok); return;
            }
            if (tries++ > 120) { self.gitlog('iframe NUNCA ficou pronto'); self.showToast('the editor did not load', 'err'); resolve(false); return; }
            setTimeout(attempt, 60);
          })();
        });
      },
      // like _gitFrameOpen, but opens the DiffEditor (gitEditorOpenDiff).
      _gitFrameOpenDiff(opts) {
        const self = this;
        return new Promise(function (resolve) {
          let tries = 0;
          (function attempt() {
            const w = self._gitFrameWin();
            if (w && typeof w.gitEditorOpenDiff === 'function' && typeof w.gitEditorReady === 'function' && w.gitEditorReady()) {
              const ok = w.gitEditorOpenDiff(Object.assign({
                theme: self.git.settings.editorTheme, fontSize: self.git.settings.fontSize, wordWrap: self.git.settings.wordWrap,
              }, opts));
              resolve(!!ok); return;
            }
            if (tries++ > 120) { self.showToast('the editor did not load', 'err'); resolve(false); return; }
            setTimeout(attempt, 60);
          })();
        });
      },
      async _gitShowDiff(path, url) {
        let diff = '';
        try { const r = await this.api(url); const d = await r.json(); diff = d.diff || '(no differences)'; }
        catch (e) { this.showToast('diff failed: ' + this._errText(e), 'err'); return; }
        const MAX = 1500000;
        if (diff.length > MAX) diff = diff.slice(0, MAX) + '\n\n… (diff truncated — large or binary file)';
        this.git.editor.open = true; this.git.editor.mode = 'diff'; this.git.editor.path = path;
        this.$nextTick(() => this._gitFrameOpen({ content: diff, language: 'diff', readOnly: true }));
      },
      async gitOpenWorkingFile(path) {
        this.gitlog('gitOpenWorkingFile INÍCIO', path);
        if (!path) return;
        if (this.gitIsFolder(path)) { this.showToast('📁 ' + path + ' is a folder — open a file', ''); return; }
        // Fetches the content FIRST; binary/directory/>8MB → backend 422.
        let content = '';
        try {
          const r = await this.api('/api/git/file?repo=' + encodeURIComponent(this.git.repo) + '&path=' + encodeURIComponent(path));
          const d = await r.json().catch(() => ({}));
          content = d.content || '';
        } catch (e) { this.showToast('could not open this file: ' + this._errText(e), 'err'); return; }
        this.gitlog('conteúdo carregado, bytes=', content.length);
        const writable = !!(this.git.repoInfo && this.git.repoInfo.writable);
        this.git.editor.open = true; this.git.editor.mode = 'edit'; this.git.editor.path = path; this.git.editor.saving = false;
        this.$nextTick(async () => {
          const ok = await this._gitFrameOpen({ content: content, language: this._langForPath(path), readOnly: !writable });
          if (ok && this.git.settings.inlineBlame) this._gitApplyInlineBlame(path);
        });
      },
      // fetches blame and pushes it into the iframe editor (inline annotation on the cursor line)
      async _gitApplyInlineBlame(path) {
        try {
          // Inline blame is decoration: a failure vanishes quietly (empty catch below).
          const r = await this.api('/api/git/blame?repo=' + encodeURIComponent(this.git.repo) + '&path=' + encodeURIComponent(path));
          const d = await r.json().catch(() => ({}));
          const w = this._gitFrameWin();
          if (w && typeof w.gitEditorSetBlame === 'function') w.gitEditorSetBlame(d.lines || []);
        } catch (e) {}
      },
      gitToggleInlineBlame() {
        this.git.settings.inlineBlame = !this.git.settings.inlineBlame;
        this.gitSaveSettings();
        const w = this._gitFrameWin();
        if (this.git.settings.inlineBlame) {
          if (this.git.editor.open && this.git.editor.mode === 'edit') this._gitApplyInlineBlame(this.git.editor.path);
        } else if (w && typeof w.gitEditorSetBlame === 'function') { try { w.gitEditorSetBlame(null); } catch (e) {} }
      },
      async gitSaveFile() {
        const w = this._gitFrameWin();
        if (!w || typeof w.gitEditorGetValue !== 'function') return;
        this.git.editor.saving = true;
        const content = w.gitEditorGetValue(), path = this.git.editor.path;
        try { await this.api('/api/git/write?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({ path: path, content: content }) }); this.showToast('saved: ' + path, 'ok'); await this.gitLoadStatus(); }
        catch (e) { this.showToast('could not save ' + path + ': ' + this._errText(e), 'err'); } finally { this.git.editor.saving = false; }
      },
      gitCloseEditor() { const w = this._gitFrameWin(); if (w && typeof w.gitEditorDispose === 'function') { try { w.gitEditorDispose(); } catch (e) {} } this.git.editor.open = false; this.git.editor.path = ''; },

      // ===== Inspection: blame / file history / compare (overlay) =====
      gitCloseInspect() { this.git.inspect.open = false; this.git.inspect.kind = ''; },
      async gitBlame(path, ref) {
        if (!path || this.gitIsFolder(path)) { this.showToast('select a file', ''); return; }
        const ins = this.git.inspect;
        Object.assign(ins, { open: true, kind: 'blame', loading: true, err: '', path: path, ref: ref || '', blame: [] });
        this.git.blameLimit = 300;
        this._scrollDisconnect('blame');
        try {
          let u = '/api/git/blame?repo=' + encodeURIComponent(this.git.repo) + '&path=' + encodeURIComponent(path);
          if (ref) u += '&ref=' + encodeURIComponent(ref);
          const r = await this.api(u); const d = await r.json().catch(() => ({}));
          ins.blame = d.lines || [];
        } catch (e) { ins.err = 'blame unavailable: ' + this._errText(e); } finally { ins.loading = false; }
      },
      async gitFileHistory(path) {
        if (!path || this.gitIsFolder(path)) { this.showToast('select a file', ''); return; }
        const ins = this.git.inspect;
        Object.assign(ins, { open: true, kind: 'filelog', loading: true, err: '', path: path, filelog: [] });
        this.git.filelogLimit = 200;
        this._scrollDisconnect('filelog');
        try {
          const r = await this.api('/api/git/filelog?repo=' + encodeURIComponent(this.git.repo) + '&path=' + encodeURIComponent(path));
          const d = await r.json().catch(() => ({}));
          ins.filelog = d.commits || [];
        } catch (e) { ins.err = 'history unavailable: ' + this._errText(e); } finally { ins.loading = false; }
      },
      // opens the compare dialog (A/B choice). Pre-fills with the current branch.
      gitCompareDialog() {
        const cur = (this.git.status && this.git.status.branch) || '';
        Object.assign(this.git.inspect, { open: true, kind: 'compare', loading: false, err: '', compare: null, a: cur, b: '', mode: 'threedot' });
      },
      async gitCompareRun() {
        const ins = this.git.inspect;
        if (!ins.a || !ins.b) { this.showToast('choose both refs', ''); return; }
        ins.loading = true; ins.err = ''; ins.compare = null;
        this.git.cmpFilesLimit = 200; this.git.cmpCommitsLimit = 200;
        this._scrollDisconnect('cmpFiles'); this._scrollDisconnect('cmpCommits');
        try {
          const u = '/api/git/compare?repo=' + encodeURIComponent(this.git.repo) +
            '&a=' + encodeURIComponent(ins.a) + '&b=' + encodeURIComponent(ins.b) + '&mode=' + encodeURIComponent(ins.mode);
          const r = await this.api(u); const d = await r.json().catch(() => ({}));
          ins.compare = d;
        } catch (e) { ins.err = 'compare failed: ' + this._errText(e); } finally { ins.loading = false; }
      },
      // click on a blame line → selects the commit in the graph (ignores
      // "Not Committed Yet", whose hash is all zeros).
      gitBlameGoto(l) {
        if (l && l.hash && /[1-9a-f]/i.test(l.hash)) { this.gitCloseInspect(); this.gitSelectCommit(l.hash); }
      },
      // list of refs (local + remote branches + tags) for the compare selects
      gitCompareRefs() {
        const out = [];
        (this.git.branches || []).forEach(b => out.push(b.name));
        (this.git.tags || []).forEach(t => out.push(t.name));
        return out;
      },

      // ===== graph theme/palette, permalink, patch =====
      gitLaneColors() { return PALETTES[this.git.settings.graphPalette] || PALETTES.vivid; },
      // GitHub base from the origin remote (owner/repo), or '' when not GitHub
      gitGithubBase() {
        const o = (this.git.remotes || []).find(r => r.name === 'origin');
        if (!o || !o.url) return '';
        const m = o.url.match(/github\.com[:/]([^/]+)\/(.+?)(?:\.git)?\/?$/);
        return m ? 'https://github.com/' + m[1] + '/' + m[2] : '';
      },
      gitPermalink(hash, path, line) {
        const base = this.gitGithubBase(); if (!base) return '';
        if (path) return base + '/blob/' + hash + '/' + path + (line ? '#L' + line : '');
        return base + '/commit/' + hash;
      },
      gitCopyPermalink(hash, path, line) {
        const u = this.gitPermalink(hash, path, line);
        if (!u) { this.showToast('remote is not GitHub', 'err'); return; }
        this.gitCopy(u); this.showToast('permalink copied', 'ok');
      },
      async gitDownloadPatch(hash) {
        try {
          const r = await this.api('/api/git/patch?repo=' + encodeURIComponent(this.git.repo) + '&hash=' + encodeURIComponent(hash));
          const d = await r.json().catch(() => ({}));
          const blob = new Blob([d.patch || ''], { type: 'text/plain' });
          const a = document.createElement('a');
          a.href = URL.createObjectURL(blob); a.download = (hash || 'commit').slice(0, 8) + '.patch';
          document.body.appendChild(a); a.click(); a.remove();
          setTimeout(() => URL.revokeObjectURL(a.href), 2000);
        } catch (e) { this.showToast('could not generate patch: ' + this._errText(e), 'err'); }
      },

      // ===== branches+, prune, tag push, stash preview, submodules =====
      async gitLoadSubmodules() {
        try {
          const r = await this.api('/api/git/submodules?repo=' + encodeURIComponent(this.git.repo));
          const d = await r.json();
          this.git.submodules = (d && d.submodules) || [];
        } catch (e) { this.git.submodules = []; }
      },
      async gitSubmoduleUpdate() {
        const d = await this._gitPost('submodule/update', {}, 'submodules updated');
        if (d) await this.gitLoadSubmodules();
      },
      async gitRemotePrune() {
        const d = await this._gitPost('remote/prune', { remote: 'origin' }, 'remote-tracking pruned');
        if (d) await this.gitLoad();
      },
      async gitTagPush(name) {
        if (!confirm('Push tag ' + name + ' to origin?')) return;
        await this._gitPost('tag/push', { name: name, remote: 'origin' }, 'tag ' + name + ' pushed');
      },
      async gitStashShow(index) {
        const ins = this.git.inspect;
        Object.assign(ins, { open: true, kind: 'stashdiff', loading: true, err: '', stashDiff: '', title: 'stash@{' + index + '}' });
        try {
          const r = await this.api('/api/git/stash/show?repo=' + encodeURIComponent(this.git.repo) + '&index=' + index);
          const d = await r.json().catch(() => ({}));
          ins.stashDiff = d.diff || '(no differences)';
        } catch (e) { ins.err = 'could not read the stash: ' + this._errText(e); } finally { ins.loading = false; }
      },
      // branch favourites (per repo, in localStorage)
      gitFavKey() { return 'vpsm_git_favs_' + this.git.repo; },
      gitLoadFavs() { try { this.git.branchFavs = JSON.parse(localStorage.getItem(this.gitFavKey()) || '[]'); } catch (e) { this.git.branchFavs = []; } },
      gitIsFav(name) { return this.git.branchFavs.indexOf(name) >= 0; },
      gitToggleFav(name) {
        const i = this.git.branchFavs.indexOf(name);
        if (i >= 0) this.git.branchFavs.splice(i, 1); else this.git.branchFavs.push(name);
        try { localStorage.setItem(this.gitFavKey(), JSON.stringify(this.git.branchFavs)); } catch (e) {}
      },
      // branches filtered by glob/substring + favourites on top
      gitFilteredBranches() {
        const q = (this.git.branchFilter || '').trim().toLowerCase();
        let list = (this.git.branches || []).slice();
        if (q) {
          // simple glob: * becomes .*  (otherwise substring)
          let re = null;
          if (q.indexOf('*') >= 0) { try { re = new RegExp('^' + q.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*') + '$', 'i'); } catch (e) {} }
          list = list.filter(b => re ? re.test(b.name) : b.name.toLowerCase().indexOf(q) >= 0);
        }
        const fav = this.git.branchFavs || [];
        list.sort((a, b) => (fav.indexOf(b.name) >= 0 ? 1 : 0) - (fav.indexOf(a.name) >= 0 ? 1 : 0));
        return list;
      },

      // ===== worktrees, linkify (Jira/markdown/URL), commit templates =====
      async gitLoadWorktrees() {
        try {
          const r = await this.api('/api/git/worktrees?repo=' + encodeURIComponent(this.git.repo));
          const d = await r.json();
          this.git.worktrees = (d && d.worktrees) || [];
        } catch (e) {}
      },
      async gitWorktreeAdd() {
        const f = this.git.worktreeForm;
        if (!f.name.trim() || !f.branch.trim()) { this.showToast('fill in name and branch', ''); return; }
        try {
          const r = await this.api('/api/git/worktree/add?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify(f) });
          const d = await r.json().catch(() => ({}));
          this.showToast('worktree created: ' + (d.path || ''), 'ok');
          this.git.worktreeForm = { name: '', branch: '', create_branch: true, base: '' };
          await this.gitLoadWorktrees(); await this.gitLoadBranches();
        } catch (e) { this.showToast('could not create worktree: ' + this._errText(e), 'err'); }
      },
      async gitWorktreeRemove(path, force) {
        if (!confirm('Remove the worktree?\n' + path + (force ? '\n\n⚠ --force (discards local changes)' : ''))) return;
        try {
          await this.api('/api/git/worktree/remove?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({ path: path, force: !!force }) });
          this.showToast('worktree removed', 'ok');
          await this.gitLoadWorktrees();
        } catch (e) { this.showToast('could not remove worktree: ' + this._errText(e), 'err'); }
      },
      // renders a commit message: escape + markdown (bold/italic/code) + URL
      // autolinking + linking of Jira keys (KEY-123 → /browse/KEY-123).
      gitRenderMsg(text) {
        if (!text) return '';
        // escape EVERYTHING first (quotes included) — the markup/links below are
        // added only on top of the already-escaped string (never on the raw
        // input), and escaping " / ' blocks attribute injection through an
        // autolink with no spaces.
        let s = String(text).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
        s = s.replace(/`([^`]+)`/g, '<code style="background:rgba(255,255,255,.08);padding:1px 4px;border-radius:4px">$1</code>');
        s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
        s = s.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>');
        s = s.replace(/(https?:\/\/[^\s<]+)/g, '<a href="$1" target="_blank" rel="noopener" style="color:var(--accent,#58a6ff)">$1</a>');
        const site = (this.jiraHealth && this.jiraHealth.site) || (this.jiraConfig && this.jiraConfig.site) || '';
        s = s.replace(/\b([A-Z][A-Z0-9]{1,9}-\d+)\b/g, function (m, k) {
          return site ? '<a href="' + site + '/browse/' + k + '" target="_blank" rel="noopener" style="color:var(--accent,#58a6ff)">' + k + '</a>' : m;
        });
        return s;
      },
      // conventional-commits template: prefixes "type(scope): " onto the message.
      gitApplyCommitType() {
        const t = this.git.commitType; if (!t) return;
        const scope = (this.git.commitScope || '').trim();
        const prefix = t + (scope ? '(' + scope + ')' : '') + ': ';
        const msg = this.git.commitMessage || '';
        // avoids duplicating when it already starts with the same type
        if (!msg.startsWith(prefix)) this.git.commitMessage = prefix + msg.replace(/^\w+(\([^)]*\))?:\s*/, '');
      },

      // ===== reflog/undo, interactive rebase, conflicts, drag-and-drop =====
      async gitReflogOpen() {
        const ins = this.git.inspect;
        Object.assign(ins, { open: true, kind: 'reflog', loading: true, err: '', reflog: [] });
        this.git.reflogLimit = 200;
        this._scrollDisconnect('reflog');
        try {
          const r = await this.api('/api/git/reflog?repo=' + encodeURIComponent(this.git.repo));
          const d = await r.json().catch(() => ({}));
          ins.reflog = d.reflog || [];
        } catch (e) { ins.err = 'reflog failed: ' + this._errText(e); } finally { ins.loading = false; }
      },
      async gitReflogReset(index, mode) {
        const warn = mode === 'hard' ? '\n\n⚠ --hard DISCARDS uncommitted changes.' : '';
        if (!confirm('Reset (--' + mode + ') to HEAD@{' + index + '}?' + warn)) return;
        try {
          const r = await this.api('/api/git/reflog/reset?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({ index: index, mode: mode }) });
          const d = await r.json().catch(() => ({}));
          this.showToast('reset → ' + (d.ref || ''), 'ok');
          this.gitCloseInspect(); await this.gitLoad();
        } catch (e) { this.showToast('reset failed: ' + this._errText(e), 'err'); }
      },
      // interactive rebase: base = the commit’s first parent (edits commits from it on)
      async gitRebaseDialog(commit) {
        const hash = (commit && commit.hash) || this.git.selected;
        if (!hash) { this.showToast('select a commit', ''); return; }
        // makes sure commitDetail is there (it carries the parents)
        if (!this.git.commitDetail || this.git.commitDetail.hash !== hash) await this.gitSelectCommit(hash);
        const parents = (this.git.commitDetail && this.git.commitDetail.parents) || [];
        if (!parents.length) { this.showToast('rebase from the root is not supported here', 'err'); return; }
        const base = parents[0];
        const ins = this.git.inspect;
        Object.assign(ins, { open: true, kind: 'rebase', loading: true, err: '', rebaseBase: base, rebaseTodo: [] });
        try {
          const r = await this.api('/api/git/rebase/todo?repo=' + encodeURIComponent(this.git.repo) + '&base=' + encodeURIComponent(base));
          const d = await r.json().catch(() => ({}));
          ins.rebaseTodo = (d.commits || []).map(c => ({ action: 'pick', hash: c.hash, short: c.short, subject: c.subject }));
        } catch (e) { ins.err = 'could not build the rebase: ' + this._errText(e); } finally { ins.loading = false; }
      },
      gitRebaseMove(i, dir) {
        const t = this.git.inspect.rebaseTodo; const j = i + dir;
        if (j < 0 || j >= t.length) return;
        const tmp = t[i]; t[i] = t[j]; t[j] = tmp;
      },
      async gitRebaseRun() {
        const ins = this.git.inspect;
        const todo = ins.rebaseTodo.map(s => ({ action: s.action, hash: s.hash }));
        if (!todo.some(s => s.action === 'pick')) { this.showToast('at least one pick is required', 'err'); return; }
        if (!confirm('Apply interactive rebase (' + todo.length + ' steps)? It rewrites history from ' + ins.rebaseBase.slice(0, 8) + '.')) return;
        ins.rebaseBusy = true;
        try {
          const r = await this.api('/api/git/rebase/interactive?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({ base: ins.rebaseBase, todo: todo }) });
          const d = await r.json().catch(() => ({}));
          if (d.ok) { this.showToast('rebase applied', 'ok'); this.gitCloseInspect(); await this.gitLoad(); }
          else if (d.conflict) { this.showToast('rebase conflict — resolve it', 'err'); await this.gitConflictsOpen(); }
          else this.showToast(this._errText(d.error) || 'rebase failed', 'err');
        } catch (e) { this.showToast('rebase failed: ' + this._errText(e), 'err'); } finally { ins.rebaseBusy = false; }
      },
      // conflicts
      async gitConflictsOpen() {
        const ins = this.git.inspect;
        Object.assign(ins, { open: true, kind: 'conflicts', loading: true, err: '', conflicts: null });
        try {
          const r = await this.api('/api/git/conflicts?repo=' + encodeURIComponent(this.git.repo));
          const d = await r.json().catch(() => ({}));
          ins.conflicts = d;
        } catch (e) { ins.err = 'could not list conflicts: ' + this._errText(e); } finally { ins.loading = false; }
      },
      async gitResolveConflict(path, strategy) {
        try {
          await this.api('/api/git/conflict/resolve?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({ path: path, strategy: strategy }) });
        } catch (e) { this.showToast('could not resolve ' + path + ': ' + this._errText(e), 'err'); return; }
        this.showToast('resolved: ' + path, 'ok');
        await this.gitConflictsOpen(); await this.gitLoadStatus();
      },
      async gitConflictEdit(path) { this.gitCloseInspect(); await this.gitOpenWorkingFile(path); },
      async gitRebaseContinue() {
        try {
          // A conflict arrives as HTTP 200 with {ok:false, conflict:true} — which is
          // why the decision is still taken from the body; the catch covers real
          // 4xx/5xx.
          const r = await this.api('/api/git/rebase/continue?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({}) });
          const d = await r.json().catch(() => ({}));
          if (d.ok) { this.showToast('rebase continued', 'ok'); this.gitCloseInspect(); await this.gitLoad(); }
          else if (d.conflict) { this.showToast('there are still conflicts', 'err'); await this.gitConflictsOpen(); }
          else this.showToast(this._errText(d.error) || 'could not continue the rebase', 'err');
        } catch (e) { this.showToast('could not continue the rebase: ' + this._errText(e), 'err'); }
      },
      async gitRebaseAbort() {
        if (!confirm('Abort the rebase or merge in progress?')) return;
        try {
          const r = await this.api('/api/git/rebase/abort?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({}) });
          const d = await r.json().catch(() => ({}));
          if (d.ok) { this.showToast('rebase aborted', 'ok'); this.gitCloseInspect(); await this.gitLoad(); }
          else this.showToast(this._errText(d.error) || 'could not abort', 'err');
        } catch (e) { this.showToast('could not abort: ' + this._errText(e), 'err'); }
      },
      // drag-and-drop: drag a commit → drop it on a branch = cherry-pick onto
      gitDragStart(hash, ev) { this.git.dragCommit = hash; try { ev.dataTransfer.effectAllowed = 'copy'; } catch (e) {} },
      gitDragEnd() { this.git.dragCommit = ''; },
      async gitDropOnBranch(branch) {
        const hash = this.git.dragCommit; this.git.dragCommit = '';
        if (!hash || !branch) return;
        if (!confirm('Cherry-pick commit ' + hash.slice(0, 8) + ' onto branch ' + branch + '?\n(checks out ' + branch + ' and applies the commit)')) return;
        try {
          const r = await this.api('/api/git/cherry-pick-onto?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({ hash: hash, branch: branch }) });
          const d = await r.json().catch(() => ({}));
          if (d.ok) { this.showToast('cherry-pick onto ' + branch, 'ok'); await this.gitLoad(); }
          else if (d.conflict) { this.showToast('conflict — resolve it', 'err'); await this.gitConflictsOpen(); }
          else this.showToast(this._errText(d.error) || 'cherry-pick failed', 'err');
        } catch (e) { this.showToast('cherry-pick failed: ' + this._errText(e), 'err'); }
      },

      // ===== per-HUNK staging =====
      // splits a unified diff into {header, hunks[]}: header = the lines up to the 1st @@.
      _parseDiffHunks(diff) {
        if (!diff) return { header: '', hunks: [] };
        const lines = diff.split('\n');
        const header = []; const hunks = []; let cur = null;
        for (const ln of lines) {
          if (ln.startsWith('@@')) { if (cur) hunks.push(cur.join('\n')); cur = [ln]; continue; }
          if (cur) cur.push(ln); else header.push(ln);
        }
        if (cur) hunks.push(cur.join('\n'));
        return { header: header.join('\n'), hunks };
      },
      async gitHunks(path) {
        if (!path || this.gitIsFolder(path)) { this.showToast('select a file', ''); return; }
        const ins = this.git.inspect;
        Object.assign(ins, { open: true, kind: 'hunks', loading: true, err: '', path: path, hunks: [] });
        this.git.hunksLimit = 200;
        this._scrollDisconnect('hunks');
        try {
          const enc = encodeURIComponent;
          const base = '/api/git/diff?repo=' + enc(this.git.repo) + '&path=' + enc(path);
          const [ru, rs] = await Promise.all([this.api(base), this.api(base + '&staged=1')]);
          const du = await ru.json().catch(() => ({})); const ds = await rs.json().catch(() => ({}));
          const un = this._parseDiffHunks(du.diff || ''); const st = this._parseDiffHunks(ds.diff || '');
          const out = [];
          un.hunks.forEach(h => out.push({ staged: false, header: un.header, hunk: h }));
          st.hunks.forEach(h => out.push({ staged: true, header: st.header, hunk: h }));
          ins.hunks = out;
        } catch (e) { ins.err = 'could not load the hunks: ' + this._errText(e); } finally { ins.loading = false; }
      },
      // colourises a hunk (HTML escape + green/red/cyan) for the <pre>.
      gitColorHunk(hunk) {
        if (!hunk) return '';
        const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
        return hunk.split('\n').map((ln) => {
          const e = esc(ln);
          if (ln.charAt(0) === '+') return '<span style="color:#3fb950">' + e + '</span>';
          if (ln.charAt(0) === '-') return '<span style="color:#f85149">' + e + '</span>';
          if (ln.indexOf('@@') === 0) return '<span style="color:#58a6ff">' + e + '</span>';
          return '<span style="color:#8b949e">' + e + '</span>';
        }).join('\n');
      },
      async gitApplyHunk(h) {
        const patch = h.header + '\n' + h.hunk;
        try {
          const r = await this.api('/api/git/apply?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify({ patch: patch, reverse: !!h.staged }) });
          await r.json().catch(() => ({}));
          this.showToast(h.staged ? 'hunk unstaged' : 'hunk staged', 'ok');
          await this.gitHunks(this.git.inspect.path);
          await this.gitLoadStatus();
        } catch (e) { this.showToast('apply failed: ' + this._errText(e), 'err'); }
      },

      // ===== generic POST =====
      async _gitPost(path, body, okMsg) {
        this.git.busy = true;
        try {
          const r = await this.api('/api/git/' + path + '?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify(body || {}) });
          const d = await r.json().catch(() => ({}));
          if (okMsg) this.showToast(okMsg, 'ok');
          return d;
          // Contract kept: null = failed (the callers test the return value).
          // The action name goes into the message — a bare "error: …" did not say
          // what had failed.
        } catch (e) { this.showToast('error in ' + path + ': ' + this._errText(e), 'err'); return null; } finally { this.git.busy = false; }
      },
      async _gitDo(path, body, okMsg, reload) {
        const d = await this._gitPost(path, body, okMsg);
        if (d && reload !== false) await this.gitLoad();
        return d;
      },

      // ===== staging / commit =====
      async gitStage(paths) { if (await this._gitPost('stage', { paths })) await this.gitLoadStatus(); },
      async gitUnstage(paths) { if (await this._gitPost('unstage', { paths })) await this.gitLoadStatus(); },
      async gitStageAll() { const ps = (this.git.status ? this.git.status.entries : []).map(e => e.path); if (ps.length) await this.gitStage(ps); },
      async gitDiscard(paths) { if (!confirm('Discard changes in ' + paths.join(', ') + '?\nThis cannot be undone.')) return; if (await this._gitPost('discard', { paths })) await this.gitLoadStatus(); },
      async gitCommit() {
        const msg = (this.git.commitMessage || '').trim();
        if (!msg && !this.git.commitAmend) return;
        const ident = this.gitSelectedIdentity();
        const signed = ident ? (ident.name + ' <' + ident.email + '>') : (this.git.repoInfo ? this.git.repoInfo.expected_identity : '?');
        const coauthors = (this.git.commitCoauthors || '').split('\n').map(s => s.trim()).filter(Boolean);
        const verb = this.git.commitAmend ? 'Amend last commit' : 'Commit';
        if (!confirm(verb + ' as ' + signed + '?' + (msg ? '\n\n"' + msg + '"' : '\n\n(keeps the current message)'))) return;
        const d = await this._gitPost('commit', {
          message: msg, identity_id: this.git.identityId,
          amend: this.git.commitAmend, signoff: this.git.commitSignoff, coauthors: coauthors,
        }, null);
        if (d) {
          this.showToast('commit ' + (d.hash || '') + ' · ' + (d.signed_as || '') + (d.identity_warn ? ' ⚠ identity mismatch' : ''), d.identity_warn ? 'err' : 'ok');
          this.git.commitMessage = ''; this.git.commitAmend = false; this.git.commitCoauthors = '';
          await this.gitLoad();
        }
      },

      // quick branch creation (Branches tab)
      async gitBranchCreate(checkout) {
        const name = (this.git.newBranch || '').trim(); if (!name) return;
        const d = await this._gitDo('branch/create', { name: name, checkout: !!checkout }, 'branch created: ' + name);
        if (d) this.git.newBranch = '';
      },

      // ===== context menu =====
      gitCtxOpen(type, data, ev) {
        ev.preventDefault(); ev.stopPropagation();
        const x = Math.min(ev.clientX, window.innerWidth - 230), y = Math.min(ev.clientY, window.innerHeight - 320);
        this.git.ctx = { open: true, x: x, y: y, type: type, data: data };
      },
      gitCloseCtx() { this.git.ctx.open = false; },
      gitCtxItems() {
        const w = !!(this.git.repoInfo && this.git.repoInfo.writable);
        const t = this.git.ctx.type, d = this.git.ctx.data || {};
        const items = [];
        if (t === 'commit') {
          items.push({ label: 'Create branch here…', fn: () => this.gitDialog('branch-create', d), w: true });
          items.push({ label: 'Create tag here…', fn: () => this.gitDialog('tag-create', d), w: true });
          items.push({ label: 'Checkout (detached)', fn: () => this._gitDo('checkout', { ref: d.hash }, 'checkout ' + (d.short || '')), w: true });
          items.push({ sep: true });
          items.push({ label: 'Cherry-pick onto current branch…', fn: () => this.gitConfirm('cherry-pick', { hash: d.hash }, 'Cherry-pick ' + (d.short || '') + ' onto the current branch?'), w: true });
          items.push({ label: 'Revert…', fn: () => this.gitConfirm('revert', { hash: d.hash }, 'Create a commit that reverts ' + (d.short || '') + '?'), w: true });
          items.push({ label: 'Merge into current branch…', fn: () => this.gitConfirm('merge', { ref: d.hash }, 'Merge ' + (d.short || '') + ' into the current branch?'), w: true });
          items.push({ label: 'Rebase current branch onto this…', fn: () => this.gitConfirm('rebase', { ref: d.hash }, 'Rebase the current branch onto ' + (d.short || '') + '?'), w: true });
          items.push({ label: 'Reset…', fn: () => this.gitDialog('reset', d), w: true });
          items.push({ label: 'Interactive rebase (from here)…', fn: () => this.gitRebaseDialog(d), w: true });
          items.push({ label: 'Drop (remove commit)…', danger: true, fn: () => this.gitConfirm('drop', { hash: d.hash }, '⚠ Remove commit ' + (d.short || '') + ' by rewriting history?', true), w: true });
          items.push({ sep: true });
          items.push({ label: 'Copy hash', fn: () => this.gitCopy(d.hash) });
          items.push({ label: 'Copy subject', fn: () => this.gitCopy(d.subject) });
        } else if (t === 'branch') {
          const name = d.name;
          items.push({ label: 'Checkout', fn: () => this._gitDo('checkout', { ref: name }, 'checkout ' + name), w: true });
          items.push({ label: 'Rename…', fn: () => this.gitDialog('branch-rename', d), w: true });
          items.push({ label: 'Delete…', danger: true, fn: () => this.gitConfirm('branch/delete', { name: name }, 'Delete branch ' + name + '?'), w: true });
          items.push({ sep: true });
          items.push({ label: 'Merge into current…', fn: () => this.gitConfirm('merge', { ref: name }, 'Merge ' + name + ' into the current branch?'), w: true });
          items.push({ label: 'Rebase onto current…', fn: () => this.gitConfirm('rebase', { ref: name }, 'Rebase the current branch onto ' + name + '?'), w: true });
          items.push({ sep: true });
          items.push({ label: 'Push…', fn: () => this.gitConfirm('push', { remote: 'origin', branch: name, set_upstream: true }, 'Push ' + name + ' to origin?'), w: true });
          items.push({ label: 'Pull…', fn: () => this.gitConfirm('pull', {}, 'Pull into the current branch?'), w: true });
          items.push({ label: 'Create Pull Request ↗', fn: () => this.gitPR(name) });
          items.push({ sep: true });
          items.push({ label: 'Copy name', fn: () => this.gitCopy(name) });
        } else if (t === 'tag') {
          items.push({ label: 'Push tag…', fn: () => this.gitTagPush(d.name), w: true });
          items.push({ label: 'Delete tag…', danger: true, fn: () => this.gitConfirm('tag/delete', { name: d.name }, 'Delete tag ' + d.name + '?'), w: true });
          items.push({ label: 'Copy name', fn: () => this.gitCopy(d.name) });
        } else if (t === 'stash') {
          items.push({ label: 'View contents (preview)', fn: () => this.gitStashShow(d.index) });
          items.push({ label: 'Apply', fn: () => this._gitDo('stash/apply', { index: d.index }, 'stash applied'), w: true });
          items.push({ label: 'Pop (apply and remove)', fn: () => this._gitDo('stash/pop', { index: d.index }, 'stash pop'), w: true });
          items.push({ label: 'Create branch from stash…', fn: () => this.gitDialog('stash-branch', d), w: true });
          items.push({ label: 'Discard (drop)…', danger: true, fn: () => this.gitConfirm('stash/drop', { index: d.index }, 'Discard the stash?'), w: true });
        }
        return items.filter(it => !it.w || w);
      },
      gitCtxClick(it) { this.gitCloseCtx(); if (it.fn) it.fn(); },

      // ===== dialogs =====
      gitDialog(kind, target) {
        const dlg = { open: true, kind: kind, target: target || {}, name: '', message: '', ref: '', mode: 'mixed', flag1: false, flag2: false, danger: false, confirmLabel: 'Confirm', body: '' };
        const titles = { 'branch-create': 'Create branch', 'branch-rename': 'Rename branch', 'tag-create': 'Create tag', 'reset': 'Reset to this commit', 'stash-push': 'Stash changes', 'stash-branch': 'Create branch from stash', 'settings': 'Appearance and settings' };
        dlg.title = titles[kind] || kind;
        if (kind === 'branch-rename') dlg.name = (target && target.name) || '';
        this.git.dialog = dlg;
      },
      gitConfirm(action, payload, body, danger) {
        this.git.dialog = { open: true, kind: 'confirm', title: 'Confirm', body: body || '', _action: action, _payload: payload || {}, danger: !!danger, confirmLabel: 'Confirm', name: '', message: '', ref: '', mode: 'mixed', flag1: false, flag2: false };
      },
      gitCloseDialog() { this.git.dialog.open = false; },
      async gitDialogConfirm() {
        const dl = this.git.dialog; const t = dl.target || {};
        this.gitCloseDialog();
        switch (dl.kind) {
          case 'confirm': await this._gitDo(dl._action, dl._payload, 'ok'); break;
          case 'branch-create': await this._gitDo('branch/create', { name: dl.name, checkout: !dl.flag1, ref: t.hash || '' }, 'branch created: ' + dl.name); break;
          case 'branch-rename': await this._gitDo('branch/rename', { name: t.name, new_name: dl.name }, 'renamed to ' + dl.name); break;
          case 'tag-create': await this._gitDo('tag/create', { name: dl.name, message: dl.message, ref: t.hash || '' }, 'tag created: ' + dl.name); break;
          case 'reset': await this._gitDo('reset', { hash: t.hash, mode: dl.mode }, 'reset --' + dl.mode); break;
          case 'stash-push': await this._gitDo('stash/push', { message: dl.message, include_untracked: dl.flag1 }, 'stash created'); break;
          case 'stash-branch': await this._gitDo('stash/branch', { index: t.index, name: dl.name }, 'branch from stash: ' + dl.name); break;
          case 'pr-create': await this.gitDoCreatePR(dl); break;
          case 'pr-edit': await this.gitDoEditPR(dl); break;
        }
      },

      // ===== toolbar actions =====
      async gitFetch() { await this._gitDo('fetch', { prune: true }, 'fetch done'); },
      async gitPull() { await this._gitDo('pull', {}, 'pull done'); },
      async gitPush() { const b = this.git.repoInfo && this.git.repoInfo.current_branch; if (!confirm('Push ' + b + ' to origin?')) return; await this._gitDo('push', { remote: 'origin', branch: b, set_upstream: true }, 'push done'); },
      async gitPR(branch) {
        branch = branch || (this.git.repoInfo && this.git.repoInfo.current_branch);
        try {
          const r = await this.api('/api/git/pr-url?repo=' + encodeURIComponent(this.git.repo) + '&branch=' + encodeURIComponent(branch));
          const d = await r.json().catch(() => ({}));
          window.open(d.url, '_blank', 'noopener');
          this.showToast('Opening PR on ' + (d.provider || 'provider') + '…', 'ok');
        } catch (e) { this.showToast('PR: ' + this._errText(e), 'err'); }
      },
      gitCopy(text) { try { navigator.clipboard.writeText(text || ''); this.showToast('copied', 'ok'); } catch (e) { this.showToast('could not copy', 'err'); } },

      // ===== Pull Requests (native, through the GitHub API) =====
      gitSetPRState(st) { this.git.prState = st; this.gitLoadPRs(); },
      async gitLoadPRs() {
        this.git.prLoading = true; this.git.prDetail = null; this.git.prView = 'list'; this.git.prErr = '';
        const st = this.git.prState || 'open';
        const apiState = (st === 'merged' || st === 'closed') ? 'closed' : st; // open|closed|all
        try {
          const r = await this.api('/api/git/pr/list?repo=' + encodeURIComponent(this.git.repo) + '&state=' + apiState);
          const d = await r.json().catch(() => ({}));
          let prs = d.prs || [];
          if (st === 'merged') prs = prs.filter(function (p) { return !!p.merged_at; });
          else if (st === 'closed') prs = prs.filter(function (p) { return !p.merged_at; });
          this.git.prs = prs; this.git.prInfo = { owner: d.owner, repo: d.repo };
        } catch (e) { this.git.prErr = this._errText(e); this.git.prs = []; }
        finally { this.git.prLoading = false; }
      },
      // a PR’s visual state: merged / closed / draft / open (colour+icon+label)
      gitPRBadge(p) {
        if (p.merged_at) return { label: 'merged', cls: 'merged' };
        if (p.state === 'closed') return { label: 'closed', cls: 'closed' };
        if (p.draft) return { label: 'draft', cls: 'remote' };
        return { label: '#' + p.number, cls: 'head' };
      },
      gitPRState2(p) {
        if (!p) return { cls: 'open', oc: 'git-pull-request', label: 'open' };
        if (p.merged_at) return { cls: 'merged', oc: 'git-merge', label: 'merged' };
        if (p.state === 'closed') return { cls: 'closed', oc: 'git-pull-request-closed', label: 'closed' };
        if (p.draft) return { cls: 'draft', oc: 'git-pull-request-draft', label: 'draft' };
        return { cls: 'open', oc: 'git-pull-request', label: 'open' };
      },
      // Octicons (official GitHub SVG icons)
      gitIcon(name, size) { return octicon(name, size); },
      gitFileIconSvg(path) { return octicon(this.gitIsFolder(path) ? 'file-directory-fill' : 'file', 13); },
      async gitOpenPR(n) {
        // Do NOT null prDetail here: if the detail is already visible (reopen/merge/
        // refresh), clearing it triggers an x-if teardown with prDetail=null and
        // the inner bindings break. We keep the previous one until the new one
        // arrives.
        this.git.prView = 'detail'; this.git.prComment = '';
        try {
          const r = await this.api('/api/git/pr/get?repo=' + encodeURIComponent(this.git.repo) + '&number=' + n);
          const d = await r.json().catch(() => ({}));
          this.git.prDetail = d.pr; this.git.prFiles = d.files || []; this.git.prComments = d.comments || [];
          this.git.prReviews = d.reviews || []; this.git.prCommits = d.commits || []; this.git.prChecks = d.checks || null;
          this.git.prReviewComments = d.review_comments || []; this.git.prFileOpen = '';
          this.gitLoadReviewed('pr:' + this.git.repo + ':' + n);
        } catch (e) { this.showToast('could not open PR: ' + this._errText(e), 'err'); this.git.prView = 'list'; }
      },
      gitChecksLabel(c) {
        if (!c || !c.total) return { txt: 'no checks', cls: 'remote' };
        if (c.state === 'success') return { txt: '✓ ' + c.passed + '/' + c.total + ' checks', cls: 'head' };
        if (c.state === 'failure') return { txt: '✗ ' + c.failed + ' failed', cls: 'closed' };
        return { txt: '● ' + c.pending + ' pending', cls: 'remote' };
      },
      gitReviewIcon(state) {
        if (state === 'APPROVED') return { t: '✓', c: '#22c55e' };
        if (state === 'CHANGES_REQUESTED') return { t: '✗', c: '#f87171' };
        return { t: '💬', c: 'var(--text-muted,#8b93a1)' };
      },
      async gitPRReview(n, event) {
        if (event !== 'APPROVE' && !(this.git.prReviewBody || '').trim()) { this.showToast('write a comment for this review', 'err'); return; }
        const labels = { APPROVE: 'approved', REQUEST_CHANGES: 'changes requested', COMMENT: 'commented' };
        const d = await this._prPost('pr/review', { number: n, event: event, body: this.git.prReviewBody }, 'review: ' + labels[event]);
        if (d) { this.git.prReviewBody = ''; await this.gitOpenPR(n); }
      },
      // ---- actions on a PR ----
      async _prPost(path, body, okMsg) {
        this.git.prBusy = true;
        try {
          const r = await this.api('/api/git/' + path + '?repo=' + encodeURIComponent(this.git.repo), { method: 'POST', body: JSON.stringify(body || {}) });
          const d = await r.json().catch(() => ({}));
          if (okMsg) this.showToast(okMsg, 'ok');
          return d;
          // Contract kept: null = failed (the callers test the return value).
        } catch (e) { this.showToast('error in ' + path + ': ' + this._errText(e), 'err'); return null; } finally { this.git.prBusy = false; }
      },
      gitPREditDialog() {
        const p = this.git.prDetail; if (!p) return;
        this.git.dialog = { open: true, kind: 'pr-edit', title: 'Edit PR #' + p.number, target: { number: p.number }, prTitle: p.title, body: p.body || '', base: p.base.ref, danger: false, confirmLabel: 'Save', name: '', message: '', head: '' };
      },
      async gitDoEditPR(dl) {
        const d = await this._prPost('pr/update', { number: dl.target.number, title: dl.prTitle, body: dl.body, base: dl.base }, 'PR updated');
        if (d && d.pr) { this.git.prDetail = d.pr; }
      },
      async gitPRSetState(n, state) {
        if (!confirm((state === 'closed' ? 'Close' : 'Reopen') + ' PR #' + n + '?')) return;
        const d = await this._prPost('pr/update', { number: n, state: state }, state === 'closed' ? 'PR closed' : 'PR reopened');
        if (d && d.pr) { this.git.prDetail = d.pr; } // only refreshes the detail (without nulling it)
      },
      async gitPRMerge(n) {
        const head = this.git.prDetail ? this.git.prDetail.head.ref : '';
        if (!confirm('Merge (' + this.git.prMergeMethod + ') PR #' + n + '?' + (this.git.prDeleteBranch ? '\nBranch ' + head + ' will be deleted.' : ''))) return;
        const d = await this._prPost('pr/merge', { number: n, method: this.git.prMergeMethod, delete_branch: this.git.prDeleteBranch, head: head }, 'PR merged (' + this.git.prMergeMethod + ')');
        if (d) { await this.gitOpenPR(n); }
      },
      async gitPRComment(n) {
        const body = (this.git.prComment || '').trim(); if (!body) return;
        const d = await this._prPost('pr/comment', { number: n, body: body }, 'comment posted');
        if (d) { this.git.prComment = ''; await this.gitOpenPR(n); }
      },
      gitPRCreateDialog() {
        const cur = this.git.repoInfo ? this.git.repoInfo.current_branch : '';
        this.git.dialog = { open: true, kind: 'pr-create', title: 'Create Pull Request (from branch ' + cur + ')', target: {}, prTitle: '', body: '', head: cur, base: 'main', reviewers: '', draft: false, danger: false, confirmLabel: 'Create PR', name: '', message: '', ref: '', mode: 'mixed', flag1: false };
      },
      async gitDoCreatePR(dl) {
        dl = dl || this.git.dialog;
        if (!dl.prTitle || !dl.prTitle.trim() || !dl.head.trim() || !dl.base.trim()) { this.showToast('title, head and base are required', 'err'); return; }
        const reviewers = (dl.reviewers || '').split(/[,\s]+/).map(s => s.trim()).filter(Boolean);
        const d = await this._gitPost('pr/create', { title: dl.prTitle, body: dl.body, head: dl.head, base: dl.base, draft: !!dl.draft, reviewers: reviewers }, null);
        if (d && d.pr) { this.showToast('PR #' + d.pr.number + ' created' + (dl.draft ? ' (draft)' : ''), 'ok'); await this.gitLoadPRs(); if (d.pr.html_url) window.open(d.pr.html_url, '_blank', 'noopener'); }
      },
      // ===== reviewed files (localStorage per context) + inline PR diff =====
      gitLoadReviewed(ctxKey) { try { this.git.reviewedSet = JSON.parse(localStorage.getItem('vpsm_git_rev_' + ctxKey) || '[]'); } catch (e) { this.git.reviewedSet = []; } this._gitRevCtx = ctxKey; },
      gitIsReviewed(path) { return this.git.reviewedSet.indexOf(path) >= 0; },
      gitToggleReviewed(path) {
        const i = this.git.reviewedSet.indexOf(path);
        if (i >= 0) this.git.reviewedSet.splice(i, 1); else this.git.reviewedSet.push(path);
        try { localStorage.setItem('vpsm_git_rev_' + this._gitRevCtx, JSON.stringify(this.git.reviewedSet)); } catch (e) {}
      },
      gitPRToggleFile(path) { this.git.prFileOpen = this.git.prFileOpen === path ? '' : path; },
      gitPRFileComments(path) { return (this.git.prReviewComments || []).filter(c => c.path === path); },
      gitOpenInGitHub(url) { if (url) window.open(url, '_blank', 'noopener'); },

      // uncommitted actions
      async gitStashUncommitted() { this.gitDialog('stash-push', {}); },
      async gitCleanUntracked() { if (!confirm('⚠ Remove ALL untracked files? This cannot be undone.')) return; await this._gitDo('clean', {}, 'cleanup done'); },
      async gitResetUncommitted() { if (!confirm('⚠ Discard ALL uncommitted changes? This cannot be undone.')) return; await this._gitDo('reset-uncommitted', {}, 'changes discarded'); },

      // ===== search (highlight + navigation, preserves the graph) =====
      gitDoFind() {
        const q = (this.git.find || '').toLowerCase().trim();
        const cs = (this.git.graph ? this.git.graph.commits : []);
        if (!q) { this.git.findHits = []; this.git.findIdx = 0; return; }
        const hits = [];
        cs.forEach(function (c, i) {
          if ((c.subject || '').toLowerCase().indexOf(q) >= 0 || (c.author || '').toLowerCase().indexOf(q) >= 0 || (c.hash || '').indexOf(q) >= 0 || (c.refs || []).join(' ').toLowerCase().indexOf(q) >= 0) hits.push(i);
        });
        this.git.findHits = hits; this.git.findIdx = 0;
        if (hits.length) this.gitScrollToRow(hits[0]);
      },
      gitFindNext(dir) { if (!this.git.findHits.length) return; this.git.findIdx = (this.git.findIdx + dir + this.git.findHits.length) % this.git.findHits.length; this.gitScrollToRow(this.git.findHits[this.git.findIdx]); },
      gitScrollToRow(i) { this.$nextTick(() => { const el = document.querySelector('[data-git-row="' + i + '"]'); if (el && el.scrollIntoView) el.scrollIntoView({ block: 'center', behavior: 'smooth' }); }); },
      gitIsHit(i) { return this.git.findHits.indexOf(i) >= 0; },

      // ===== dates =====
      gitRelDate(iso) { if (!iso) return ''; const d = new Date(iso), s = (Date.now() - d.getTime()) / 1000; if (s < 60) return 'now'; if (s < 3600) return Math.floor(s / 60) + 'min'; if (s < 86400) return Math.floor(s / 3600) + 'h'; if (s < 2592000) return Math.floor(s / 86400) + 'd'; return d.toLocaleDateString(); },
      gitFmtDate(iso) { if (!iso) return ''; try { return new Date(iso).toLocaleString(); } catch (e) { return iso; } },
    };
  };
})();
