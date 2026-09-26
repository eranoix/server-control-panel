// 20-deploy.js — the Deploy tab module (Heroku-style PaaS). An IIFE that
// exposes window.VPSMDeployModule(); the object is spread inside app() in
// 00-shell.js, so `this` here is the Alpine component (this.api, this.showToast,
// this.openJobLog, this.askConfirm and this._errText are all available).
(function () {
  window.VPSMDeployModule = function () {
    return {
      deploy: {
        apps: [], repoRoot: '', host: '', loading: false, forbidden: false,
        showCreate: false,
        form: { name: '', port: '', domain: '', branch: 'main', compose_file: 'docker-compose.yml' },
        openApp: '',
        env: { rows: [], preview: false, dirty: false },
        catalog: [], showCatalog: false, catSel: null,
        catForm: { name: '', port: '', domain: '', env: [] },
        ports: [], portPrefix: '/_port/', manualPort: '',
        menuOpen: '', // name of the app whose "⋯" menu (secondary actions) is open
        running: [],  // names of apps with a deploy in flight — see isDeploying()/deployRun()
      },

      deployInit() { this.loadDeployApps(); this.loadDevPorts(); },

      // ---- editor port forwarding ----
      async loadDevPorts() {
        try {
          // raw:true — a 403 is deliberate silence (user without the editor enabled),
          // not an error to show.
          const r = await this.api('/api/dev/ports', { raw: true });
          if (r.status === 403) { this.deploy.ports = []; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json().catch(() => ({}));
          this.deploy.ports = d.ports || [];
          this.deploy.portPrefix = d.prefix || '/_port/';
        } catch (e) { console.warn('[deploy] ports', e); }
      },
      deployPortURL(n) { return `${location.origin}${this.deploy.portPrefix || '/_port/'}${n}/`; },
      deployOpenPort(n) { if (n) window.open(this.deployPortURL(n), '_blank', 'noopener'); },
      async deployCopyPortURL(n) {
        try { await navigator.clipboard.writeText(this.deployPortURL(n)); this.showToast('URL copied', 'ok'); }
        catch (e) { this.showToast('copy it manually', 'warn'); }
      },

      async loadDeployApps() {
        this.deploy.loading = true;
        try {
          // raw:true — a 403 feeds the `forbidden` state (access-denied screen), it is
          // not a load failure.
          const r = await this.api('/api/deploy/apps', { raw: true });
          if (r.status === 403) { this.deploy.forbidden = true; this.deploy.apps = []; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.deploy.apps = d.apps || [];
          this.deploy.repoRoot = d.repo_root || '/srv/vpsm-apps';
          this.deploy.host = location.hostname;
          this.deploy.forbidden = false;
          // If some app has a deploy sitting in 'building' (e.g. a git push that never
          // went through the UI), start the poll so the badge leaves "building" by itself.
          if ((this.deploy.apps || []).some(a => (a.deploys || []).some(dp => dp.status === 'building'))) this._ensureDeployPoll();
        } catch (e) { console.warn('[deploy] load', e); }
        finally { this.deploy.loading = false; }
      },

      deployLastStatus(app) {
        const ds = app.deploys || [];
        // the last PRODUCTION deploy (ignores previews/teardowns, which would
        // otherwise make a running app look "rolled_back").
        for (let i = ds.length - 1; i >= 0; i--) {
          if (!ds[i].preview) return ds[i];
        }
        return null;
      },
      deployBadgeStyle(s) {
        const c = s === 'running' ? '#22c55e' : s === 'failed' ? '#ef4444'
          : s === 'building' ? '#f59e0b' : s === 'rolled_back' ? '#8b5cf6' : '#64748b';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      deployShort(c) { return (c || '').slice(0, 10); },
      deployFmtTime(ts) {
        if (!ts) return '—';
        try { return new Date(ts * 1000).toLocaleString(); } catch (e) { return '' + ts; }
      },
      deployRemoteCmd(app) {
        return `git remote add vpsm root@${this.deploy.host}:${this.deploy.repoRoot}/${app.name}.git`;
      },
      async deployCopyRemote(app) {
        try { await navigator.clipboard.writeText(this.deployRemoteCmd(app)); this.showToast('command copied', 'ok'); }
        catch (e) { this.showToast('copy the command manually', 'warn'); }
      },

      async deployCreate() {
        const f = this.deploy.form;
        if (!f.name) { this.showToast('name is required', 'err'); return; }
        let d;
        try {
          const r = await this.api('/api/deploy/apps', {
            method: 'POST', body: JSON.stringify({
              name: f.name, port: Number(f.port) || 0, domain: f.domain,
              branch: f.branch || 'main', compose_file: f.compose_file || 'docker-compose.yml',
            }),
          });
          d = await r.json().catch(() => ({}));
        } catch (e) { this.showToast('could not create app: ' + this._errText(e), 'err'); return; }
        this.showToast('app created — run git push to publish', 'ok');
        this.deploy.showCreate = false;
        this.deploy.form = { name: '', port: '', domain: '', branch: 'main', compose_file: 'docker-compose.yml' };
        await this.loadDeployApps();
        if (d.app) this.deploy.openApp = d.app.name;
      },

      // Deploy reentrancy GUARD, PER APP. A double click on the button would fire
      // two POST /api/deploy/app/deploy and the backend would queue two concurrent
      // builds on the SAME work-tree (checkout and build treading on each other).
      // The risk is per app — different apps are different work-trees and can build
      // in parallel —, so the state is the list of names in progress and not a
      // global boolean. It lives here in the JS because :disabled in the HTML covers
      // neither a repeated Enter nor a click before the next render frame.
      // A list (not a Set), so as not to depend on collection reactivity in Alpine.
      // "is it deploying?" derives from the real JOB, not only from the POST that
      // queues it. `deploy.running` stays as the IMMEDIATE reentrancy guard (between
      // the click and the job showing up in this.jobs); from there on the truth comes
      // from the queue (kind app_deploy + args.app + status running/queued), so the
      // badge/button follow the whole build — deploys via git push included.
      isDeploying(app) {
        const n = app && app.name ? app.name : app;
        if ((this.deploy.running || []).indexOf(n) >= 0) return true;
        return this._deployJobActiveFor(n);
      },
      _deployJobActiveFor(name) {
        return (this.jobs || []).some(j => j.kind === 'app_deploy' && j.args && j.args.app === name && (j.status === 'running' || j.status === 'queued'));
      },
      _activeDeployJobs() {
        return (this.jobs || []).filter(j => j.kind === 'app_deploy' && (j.status === 'running' || j.status === 'queued'));
      },
      // Starts the 5s poll while there is an active build; stops it when none is left.
      // Called by loadJobs() (source of truth for the queue) and by deployRun/loadDeployApps.
      _deployPollReconcile() {
        if (this._activeDeployJobs().length > 0) this._ensureDeployPoll();
        else this._stopDeployPoll();
      },
      _ensureDeployPoll() {
        if (this.deployPollTimer) return;
        this.deployPollTimer = setInterval(async () => {
          if (document.hidden) return; // visibility guard: do not hit the API while the tab is hidden
          try { await this.loadJobs(); } catch (_) {}                                              // redo the job correlation
          if (this.currentView === 'deploy') { try { await this.loadDeployApps(); } catch (_) {} } // badge leaves "building"
          if (this._activeDeployJobs().length === 0) this._stopDeployPoll();
        }, 5000);
      },
      _stopDeployPoll() {
        if (this.deployPollTimer) { clearInterval(this.deployPollTimer); this.deployPollTimer = null; }
      },
      async deployRun(app) {
        const name = app && app.name ? app.name : app;
        if (this.isDeploying(name)) return;
        this.deploy.running.push(name);
        try {
          const r = await this.api('/api/deploy/app/deploy', { method: 'POST', body: JSON.stringify({ name }) });
          const d = await r.json().catch(() => ({}));
          this.showToast('deploy started', 'ok');
          if (d.job) this.openJobLog({ id: d.job, status: 'running', kind: 'app_deploy' });
          // The POST answers on QUEUEING (~ms), not when the build finishes. It reloads
          // the queue (the job arrives as queued/running → isDeploying starts deriving
          // from it) and starts the 5s poll that follows the badge until the build ends.
          await this.loadJobs();
          this._deployPollReconcile();
          this.loadDeployApps();
        } catch (e) {
          this.showToast('could not start deploy: ' + this._errText(e), 'err');
        } finally {
          // Releases the immediate reentrancy guard. This does not reopen a double-click
          // window: by now the job is already in this.jobs, so isDeploying stays true.
          const i = this.deploy.running.indexOf(name);
          if (i >= 0) this.deploy.running.splice(i, 1);
        }
      },

      deployRollback(app) {
        this.askConfirm('Rollback', `Roll "${app.name}" back to the previous deploy?`, async () => {
          try {
            const r = await this.api('/api/deploy/app/rollback', { method: 'POST', body: JSON.stringify({ name: app.name }) });
            const d = await r.json().catch(() => ({}));
            this.showToast('rollback started', 'ok');
            if (d.job) this.openJobLog({ id: d.job, status: 'running', kind: 'app_deploy' });
            // A rollback is an app_deploy job too: reload the queue and start the poll.
            await this.loadJobs();
            this._deployPollReconcile();
            this.loadDeployApps();
          } catch (e) { this.showToast('rollback failed: ' + this._errText(e), 'err'); }
        });
      },

      deployDestroy(app) {
        this.askConfirm('Destroy app',
          `Tears down the containers and deletes the repo and work-tree of "${app.name}". Irreversible.`,
          async () => {
            try {
              await this.api('/api/deploy/app/destroy', { method: 'POST', body: JSON.stringify({ name: app.name }) });
            } catch (e) { this.showToast('could not destroy: ' + this._errText(e), 'err'); return; }
            this.showToast('app destroyed', 'ok');
            if (this.deploy.openApp === app.name) this.deploy.openApp = '';
            this.loadDeployApps();
          });
      },

      deployToggle(app) {
        if (this.deploy.openApp === app.name) { this.deploy.openApp = ''; return; }
        this.deploy.openApp = app.name;
        this.deploy.env.preview = false;
        this.loadDeployEnv(app);
      },

      async loadDeployEnv(app) {
        try {
          const r = await this.api('/api/deploy/app/env?name=' + encodeURIComponent(app.name));
          const d = await r.json().catch(() => ({}));
          const src = this.deploy.env.preview ? (d.preview_env || {}) : (d.env || {});
          this.deploy.env.rows = Object.keys(src).sort().map((k) => ({ k, v: src[k] }));
          this.deploy.env.dirty = false;
        } catch (e) { console.warn('[deploy] env', e); }
      },
      deployEnvAdd() { this.deploy.env.rows.push({ k: '', v: '' }); this.deploy.env.dirty = true; },
      deployEnvDel(i) { this.deploy.env.rows.splice(i, 1); this.deploy.env.dirty = true; },
      async deployEnvTogglePreview(app, preview) {
        this.deploy.env.preview = preview;
        await this.loadDeployEnv(app);
      },
      async deployEnvSave(app) {
        const set = {};
        for (const row of this.deploy.env.rows) { if (row.k) set[row.k] = row.v; }
        try {
          await this.api('/api/deploy/app/env', {
            method: 'POST', body: JSON.stringify({ name: app.name, preview: this.deploy.env.preview, set }),
          });
        } catch (e) { this.showToast('could not save env: ' + this._errText(e), 'err'); return; }
        this.showToast('env saved (applies on the next deploy)', 'ok');
        this.deploy.env.dirty = false;
      },

      // ---- service catalogue ----
      async deployOpenCatalog() {
        this.deploy.showCatalog = true;
        this.deploy.catSel = null;
        if (!this.deploy.catalog.length) {
          try {
            const r = await this.api('/api/deploy/catalog');
            const d = await r.json().catch(() => ({}));
            this.deploy.catalog = d.templates || [];
          } catch (e) { console.warn('[deploy] catalog', e); }
        }
      },
      deployPickTemplate(t) {
        this.deploy.catSel = t;
        this.deploy.catForm = {
          name: t.id, port: t.port || '', domain: '',
          env: (t.env_hints || []).map((h) => ({ k: h.key, v: h.default || '', note: h.note || '', secret: !!h.secret })),
        };
      },
      async deployCreateFromTemplate() {
        const f = this.deploy.catForm, t = this.deploy.catSel;
        if (!t || !f.name) { this.showToast('choose a template and a name', 'err'); return; }
        const env = {};
        for (const row of f.env) { if (row.k) env[row.k] = row.v; }
        let d;
        try {
          const r = await this.api('/api/deploy/catalog/create', {
            method: 'POST', body: JSON.stringify({
              template_id: t.id, name: f.name, port: Number(f.port) || 0, domain: f.domain, env,
            }),
          });
          d = await r.json().catch(() => ({}));
        } catch (e) { this.showToast('could not create from template: ' + this._errText(e), 'err'); return; }
        this.showToast('service created — starting up', 'ok');
        this.deploy.showCatalog = false;
        if (d.job) this.openJobLog({ id: d.job, status: 'running' });
        setTimeout(() => this.loadDeployApps(), 6000);
      },

      // ---- preview envs ----
      deployActivePreviews(app) {
        const running = {}, started = {}, commit = {};
        for (const d of (app.deploys || [])) {
          if (!d.preview || d.status !== 'running') continue;
          running[d.preview] = (running[d.preview] || 0) + 1;
          if ((d.started || 0) >= (started[d.preview] || 0)) { started[d.preview] = d.started; commit[d.preview] = d.commit; }
        }
        return Object.keys(running).map((slug) => ({ slug, started: started[slug], commit: commit[slug] }));
      },
      deployPreviewURL(app, slug) {
        return app.domain ? `http://${slug}.${app.domain}/` : '';
      },
      deployPreviewTeardown(app, slug) {
        this.askConfirm('Tear down preview', `Tear down preview "${slug}" of ${app.name}?`, async () => {
          try {
            await this.api('/api/deploy/app/preview/teardown', {
              method: 'POST', body: JSON.stringify({ name: app.name, slug }),
            });
          } catch (e) { this.showToast('could not tear down preview: ' + this._errText(e), 'err'); return; }
          this.showToast('preview torn down', 'ok');
          this.loadDeployApps();
        });
      },
    };
  };
})();
