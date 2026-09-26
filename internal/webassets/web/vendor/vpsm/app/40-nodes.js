// 40-nodes.js — the nodes DATA LAYER (multi-node inventory). An IIFE that
// exposes window.VPSMNodesModule(); the object is spread inside app() in
// 00-shell.js, so `this` here is the Alpine component (this.api, this.showToast,
// this.askConfirm and this._errText already exist). Mould: 20-deploy.js.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 THE "Nodes" SCREEN NO LONGER EXISTS — and this file remains.
//
// Its own tab was MERGED into the Proxmox tab: the hypervisor is one node among
// the others, and two screens for the same subject forced the operator to
// remember which of them held the information he wanted. What draws today is
// 41-proxmox.js.
//
// What stayed here is what always belonged here: FETCHING from the server and
// TRANSLATING the states the server distinguishes. Re-copying `nodesCredLabel`
// and its four readings into 41-proxmox.js would create two truths about what a
// revoked credential is — and the second would age in silence.
//
// The file’s own timer (nodesStartPoll/nodesStopPoll) was REMOVED, and the
// removal is the point: it was guarded by `currentView !== 'nodes'`, a condition
// no navigation satisfies any more. A timer that never fires is code that looks
// like it works. What now calls loadNodes() periodically is the Proxmox tab’s
// poll, which is the screen where the nodes appear.
// ────────────────────────────────────────────────────────────────────────────
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 THE RULE THAT GOVERNS THIS FILE: age is NOT computed here.
//
// The server sends `age_seconds` and `stale` already resolved (inventory.View,
// at the instant of serialisation). This project spans two machines and a
// tailnet, and the clock on the operator’s machine is NOT a controlled variable:
// if it runs fast, everything shows up expired; if it runs slow, a dead node
// shows up alive. That is why there is not, and cannot come to be, a
// `Date.now()/1000 - observed_at` in this file. formatAge() FORMATS a number
// that arrived ready.
//
// 🔴 AND THE SECOND: zero credential caching.
// "The screen shows 'no credential' and not a cached value" is the literal
// wording of the acceptance criterion. The state comes from the server on every
// load; nothing is memorised here.
// ────────────────────────────────────────────────────────────────────────────
(function () {
  window.VPSMNodesModule = function () {
    return {
      nodes: {
        list: [],          // NodeViews exactly as the server delivered them
        loading: false,
        forbidden: false,
        vault: 'ok',       // 'ok' | 'inalcancavel' — GLOBAL state of the vault
        ttl: 90,
        open: '',          // id of the node whose detail is open
        detail: null,      // { node, services, deployments, jobs }
        busy: '',          // id of the node with an action in flight (reentrancy guard)
        lastError: '',     // last server error, WHOLE, visible on screen
        serverNow: 0,      // the SERVER’s clock, carried in the payload — see nodesCredExpiry
        // 🔴 THE SECOND CLOCK (pollclock.go). Each node’s `age_seconds` says how
        // long the panel has KNOWN that; `poll` says how long ago it ASKED, and
        // brings the reason the last attempt failed. With a single clock, "the
        // node went silent" and "the poller stopped" are the same screen — and
        // the second hypothesis accuses ALL the nodes at once, every one of them
        // innocent. It arrives READY from the server, like every age in this
        // project.
        poll: null,        // { observed_at, age_seconds, stale, error }
      },

      nodesInit() { this.loadNodes(); },

      async loadNodes() {
        this.nodes.loading = true;
        try {
          // raw:true — a 403 feeds the `forbidden` state (the 20-deploy.js mould),
          // it is not a load failure.
          const r = await this.api('/api/nodes', { raw: true });
          if (r.status === 403) { this.nodes.forbidden = true; this.nodes.list = []; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.nodes.list = d.nodes || [];
          this.nodes.vault = d.vault || 'ok';
          this.nodes.ttl = d.ttl_seconds || 90;
          this.nodes.serverNow = d.observed_at || 0;
          this.nodes.poll = d.poll || null;
          this.nodes.forbidden = false;
          this.nodes.lastError = '';
        } catch (e) {
          this.nodes.lastError = this._errText(e);
          console.warn('[nodes] load', e);
        } finally { this.nodes.loading = false; }
      },

      // ---- age and freshness ----------------------------------------------

      // formatAge receives age_seconds ALREADY COMPUTED BY THE SERVER.
      // -1 is the marker for "never observed" — and it must NOT turn into "now":
      // "data from 0s ago" reads as just-seen, which is exactly the stale data
      // presented as live that the acceptance criterion exists to forbid.
      nodesFormatAge(ageSeconds) {
        if (ageSeconds === null || ageSeconds === undefined) return 'no timestamp';
        if (ageSeconds < 0) return 'never observed';
        if (ageSeconds < 60) return `data from ${ageSeconds}s ago`;
        const min = Math.floor(ageSeconds / 60);
        if (min < 60) return `data from ${min} min ago`;
        const h = Math.floor(min / 60);
        if (h < 48) return `data from ${h}h ago`;
        return `data from ${Math.floor(h / 24)} days ago`;
      },

      nodesStaleBadge(n) {
        if (!n) return '';
        return n.stale ? 'expired' : 'live';
      },
      nodesStaleStyle(n) {
        const c = (n && n.stale) ? '#f59e0b' : '#22c55e';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      nodesStatusStyle(n) {
        const v = n && n.status ? n.status.value : '';
        const c = v === 'running' || v === 'online' ? '#22c55e' : v === 'stopped' ? '#64748b' : '#f59e0b';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      nodesTransportBadge(n) { return (n && n.transport) || '—'; },

      // ---- credential ------------------------------------------------------

      // nodesCredLabel translates the FOUR states the server distinguishes. Merging
      // two of them here would undo what the backend separated on purpose:
      //   ausente  = there never was a token for this node
      //   revogada = the operator revoked it (DELETE on the PVE + vault), a recorded action
      //   expirada = the `expire` date went by; that is the CALENDAR, not security
      //   ok       = token alive and within its validity
      nodesCredLabel(n) {
        const s = n && n.credential ? n.credential.state : '';
        if (s === 'ausente') return 'no credential (missing)';
        if (s === 'revogada') return 'no credential (revoked)';
        if (s === 'expirada') return 'credential expired';
        if (s === 'ok') return 'credential ok';
        return 'credential unknown';
      },
      nodesCredStyle(n) {
        const s = n && n.credential ? n.credential.state : '';
        const c = s === 'ok' ? '#22c55e' : s === 'expirada' ? '#f59e0b' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      // nodesCredExpiry is the VISUAL sibling of the standing alarm: the PVE’s 401
      // cannot tell revoked from expired apart, so what warns beforehand is the
      // date the panel stored. Amber under 30 days.
      // The arithmetic uses the SERVER’s clock (`observed_at` from the payload),
      // not the browser’s — for the same reason as the age. Without `serverNow`
      // the answer is empty: we would rather say nothing than state a deadline
      // measured by the wrong clock.
      nodesCredDias(n) {
        const c = n && n.credential ? n.credential : null;
        if (!c || !c.expire || !this.nodes.serverNow) return null;
        return Math.floor((c.expire - this.nodes.serverNow) / 86400);
      },
      nodesCredExpiry(n) {
        const dias = this.nodesCredDias(n);
        if (dias === null) return '';
        if (dias < 0) return `expired ${-dias} days ago`;
        if (dias <= 30) return `expires in ${dias} days`;
        return '';
      },
      nodesCredExpiryUrgente(n) {
        const dias = this.nodesCredDias(n);
        return dias !== null && dias <= 30;
      },

      // ---- detail ----------------------------------------------------------

      async nodesToggle(n) {
        if (this.nodes.open === n.id) { this.nodes.open = ''; this.nodes.detail = null; return; }
        this.nodes.open = n.id;
        this.nodes.detail = null;
        try {
          const r = await this.api('/api/nodes/' + n.id);
          this.nodes.detail = await r.json().catch(() => null);
        } catch (e) {
          this.nodes.lastError = this._errText(e);
          this.showToast('failed to open the node: ' + this._errText(e), 'err');
        }
      },

      // ---- actions ---------------------------------------------------------

      nodesCanPower(n) { return n && n.kind === 'guest' && n.vmid > 0; },
      nodesBusy(n) { return this.nodes.busy === (n && n.id); },

      // nodesPower confirms BEFORE and waits for the WHOLE answer. The server only
      // replies after the WaitTask (status stopped + exitstatus OK), so the
      // spinner covers the real operation, not the "request accepted".
      nodesPower(n, action) {
        const rotulos = { start: 'Turn on', stop: 'Power off (cuts the power)', shutdown: 'Shut down gracefully' };
        const aviso = action === 'stop'
          ? `Cuts the power to "${n.name}" outright — the same as pulling the cable.`
          : `Runs "${action}" on "${n.name}". The response only comes back once the hypervisor has finished the task.`;
        this.askConfirm(rotulos[action] || action, aviso, async () => {
          if (this.nodes.busy) return;
          this.nodes.busy = n.id;
          this.nodes.lastError = '';
          try {
            const r = await this.api('/api/nodes/' + n.id + '/power', {
              method: 'POST', body: JSON.stringify({ action }),
            });
            const d = await r.json().catch(() => ({}));
            this.showToast(`${action} finished (task ${d.upid || '—'})`, 'ok');
          } catch (e) {
            // The error body carries the PVE’s exitstatus or the step that failed —
            // and it goes to the screen WHOLE. A summarised error is an error
            // that does not help.
            const txt = this._errText(e);
            this.nodes.lastError = txt;
            this.showToast('failure on ' + action + ': ' + txt, 'err');
          } finally {
            this.nodes.busy = '';
            await this.loadNodes();
          }
        });
      },

      // nodesRevoke is the visible end of the revocation decision. Its warning is
      // the strongest on the screen because the operation is irreversible on BOTH
      // sides: the token disappears from the hypervisor AND from the vault, and
      // there is no recreating it without the applier.
      nodesRevoke(n) {
        this.askConfirm('Revoke the credential',
          `Deletes the token for "${n.name}" on the hypervisor AND in the vault, in that order. ` +
          `Irreversible: the node is left with no credential until someone runs the applier again.`,
          async () => {
            if (this.nodes.busy) return;
            this.nodes.busy = n.id;
            this.nodes.lastError = '';
            try {
              const r = await this.api('/api/nodes/' + n.id + '/credential', { method: 'DELETE' });
              const d = await r.json().catch(() => ({}));
              this.showToast('credential revoked (' + (d.passos || []).join(' → ') + ')', 'ok');
            } catch (e) {
              // The error response NAMES the step that failed (pve.delete,
              // pve.confirm401, vault.delete, vault.recheck). Without it the
              // operator cannot tell whether the token died on the hypervisor.
              const txt = this._errText(e);
              this.nodes.lastError = txt;
              this.showToast('revocation failed: ' + txt, 'err');
            } finally {
              this.nodes.busy = '';
              // Reload from the SERVER: the new state comes from there, never from here.
              await this.loadNodes();
              if (this.nodes.open === n.id) { this.nodes.open = ''; await this.nodesToggle(n); }
            }
          });
      },

      // ---- periodic refresh ------------------------------------------------
      //
      // 🔴 THERE IS NO TIMER HERE, and the absence is deliberate.
      //
      // There was one: a 15 s `setInterval` guarded by `currentView !== 'nodes'`.
      // With the screen merged into the Proxmox tab, `currentView` is never
      // 'nodes' again — the timer would go on existing, being created and never
      // doing anything. Code that looks like it takes care of something and does
      // not is worse than absent code, because nobody will look for the defect
      // in it.
      //
      // What re-fetches periodically is `pvxStartPoll` (41-proxmox.js), on the
      // screen where the nodes appear. And it re-FETCHES from the server: no
      // age, no freshness and no credential is recomputed in the browser.
    };
  };
})();
