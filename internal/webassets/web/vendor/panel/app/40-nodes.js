(function () {
  window.PanelNodesModule = function () {
    return {
      nodes: {
        list: [],
        loading: false,
        forbidden: false,
        vault: 'ok',
        ttl: 90,
        open: '',
        detail: null,
        busy: '',
        lastError: '',
        serverNow: 0,
        poll: null,
      },

      nodesInit() { this.loadNodes(); },

      async loadNodes() {
        this.nodes.loading = true;
        try {
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

      nodesCredLabel(n) {
        const s = n && n.credential ? n.credential.state : '';
        if (s === 'absent') return 'no credential (missing)';
        if (s === 'revoked') return 'no credential (revoked)';
        if (s === 'expired') return 'credential expired';
        if (s === 'ok') return 'credential ok';
        return 'credential unknown';
      },
      nodesCredStyle(n) {
        const s = n && n.credential ? n.credential.state : '';
        const c = s === 'ok' ? '#22c55e' : s === 'expired' ? '#f59e0b' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      nodesCredDays(n) {
        const c = n && n.credential ? n.credential : null;
        if (!c || !c.expire || !this.nodes.serverNow) return null;
        return Math.floor((c.expire - this.nodes.serverNow) / 86400);
      },
      nodesCredExpiry(n) {
        const days = this.nodesCredDays(n);
        if (days === null) return '';
        if (days < 0) return `expired ${-days} days ago`;
        if (days <= 30) return `expires in ${days} days`;
        return '';
      },
      nodesCredExpiryUrgent(n) {
        const days = this.nodesCredDays(n);
        return days !== null && days <= 30;
      },

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

      nodesCanPower(n) { return n && n.kind === 'guest' && n.vmid > 0; },
      nodesBusy(n) { return this.nodes.busy === (n && n.id); },

      nodesPower(n, action) {
        const labels = { start: 'Turn on', stop: 'Power off (cuts the power)', shutdown: 'Shut down gracefully' };
        const warning = action === 'stop'
          ? `Cuts the power to "${n.name}" outright — the same as pulling the cable.`
          : `Runs "${action}" on "${n.name}". The response only comes back once the hypervisor has finished the task.`;
        this.askConfirm(labels[action] || action, warning, async () => {
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
            const txt = this._errText(e);
            this.nodes.lastError = txt;
            this.showToast('failure on ' + action + ': ' + txt, 'err');
          } finally {
            this.nodes.busy = '';
            await this.loadNodes();
          }
        });
      },

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
              this.showToast('credential revoked (' + (d.steps || []).join(' → ') + ')', 'ok');
            } catch (e) {
              const txt = this._errText(e);
              this.nodes.lastError = txt;
              this.showToast('revocation failed: ' + txt, 'err');
            } finally {
              this.nodes.busy = '';
              await this.loadNodes();
              if (this.nodes.open === n.id) { this.nodes.open = ''; await this.nodesToggle(n); }
            }
          });
      },

    };
  };
})();
