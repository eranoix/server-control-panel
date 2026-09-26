// 30-agents.js — the Agents tab module: a kanban of sessions by state plus a
// platform-level cost dashboard. An IIFE spread into app() (00-shell.js), so
// `this` is the Alpine component (this.api, this.showToast, this.timeAgo and
// this._fmtTok are already available).
(function () {
  window.VPSMAgentsModule = function () {
    return {
      agents: {
        sessions: [], counts: {}, totalCost: 0, loading: false, forbidden: false,
        accounts: [], accountsTotal: { today: 0, week: 0, total: 0 },
      },

      // kanban columns, in display order
      agentCols: [
        { key: 'running', label: 'Running', color: '#22c55e' },
        { key: 'waiting_input', label: 'Waiting for input', color: '#f59e0b' },
        { key: 'done', label: 'Done', color: '#3b82f6' },
        { key: 'idle', label: 'Idle', color: '#64748b' },
        { key: 'error', label: 'Error', color: '#ef4444' },
      ],

      agentsInit() { this.loadAgents(); this.loadAgentAccounts(); },

      async loadAgents() {
        this.agents.loading = true;
        try {
          // raw:true — a 403 here is not an error, it is "user without agent
          // permission": the tab shows the access-denied notice instead of a
          // failure toast.
          const r = await this.api('/api/agent/sessions', { raw: true });
          if (r.status === 403) { this.agents.forbidden = true; this.agents.sessions = []; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.agents.sessions = d.sessions || [];
          this.agents.counts = d.counts || {};
          this.agents.totalCost = d.total_cost_usd || 0;
          this.agents.forbidden = false;
        } catch (e) { console.warn('[agents] load', e); }
        finally { this.agents.loading = false; }
      },

      async loadAgentAccounts() {
        try {
          // The cost panel is an accessory: any failure becomes a console warn and the
          // section stays empty — api() throws and the catch below absorbs it.
          const r = await this.api('/api/claude/accounts/usage');
          const d = await r.json();
          const accs = d.accounts || [];
          this.agents.accounts = accs;
          const sum = (f) => accs.reduce((a, x) => a + ((x[f] && x[f].cost_usd) || 0), 0);
          this.agents.accountsTotal = { today: sum('today'), week: sum('last_7d'), total: sum('total') };
        } catch (e) { console.warn('[agents] accounts', e); }
      },

      agentCol(state) { return this.agents.sessions.filter((s) => s.state === state); },
      agentColCount(state) { return this.agents.counts[state] || 0; },
      agentFmtCost(n) { return '$' + (Number(n) || 0).toFixed(2); },
      agentTok(s) {
        if (!s.tokens) return '';
        const t = (s.tokens.in || 0) + (s.tokens.out || 0) + (s.tokens.cache_read || 0) + (s.tokens.cache_creation || 0);
        return this._fmtTok ? this._fmtTok(t) : String(t);
      },
      agentAgo(ts) {
        if (!ts) return '';
        try { return this.timeAgo ? this.timeAgo(ts) : new Date(ts * 1000).toLocaleString(); }
        catch (e) { return ''; }
      },
      agentShortName(n) { return (n || '').length > 28 ? n.slice(0, 27) + '…' : n; },
      agentOpen(s) {
        if (s.cwd) window.open('/_code/?folder=' + encodeURIComponent(s.cwd), '_blank', 'noopener');
        else this.showToast('no known working directory for this session', 'warn');
      },
      async agentCopyName(s) {
        try { await navigator.clipboard.writeText(s.name); this.showToast('name copied', 'ok'); }
        catch (e) { this.showToast('copy it by hand: ' + s.name, 'warn'); }
      },
    };
  };
})();
