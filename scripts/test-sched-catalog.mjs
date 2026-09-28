#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const SRC = join(here, '..', 'internal', 'webassets', 'web', 'vendor', 'panel', 'app', '00-shell.js');
const src = readFileSync(SRC, 'utf8');

function extract(name, sig) {
  const re = new RegExp(name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\(' + sig + '\\) \\{([\\s\\S]*?)\\n    \\},');
  const m = src.match(re);
  if (!m) { console.error('FATAL: method not found:', name); process.exit(2); }
  return m[1];
}

const comp = {
  schedCatalog: [
    { kind: 'apt_upgrade', label: 'Update packages', requires_primary: true, schedulable: true, args: [] },
    { kind: 'docker_pull', label: 'Pull Docker image', schedulable: true,
      args: [{ name: 'ref', label: 'Image', type: 'string', required: true }] },
    { kind: 'docker_compose_pull', label: 'Compose pull', schedulable: true,
      args: [{ name: 'dir', label: 'Directory', type: 'folder', required: true }] },
    { kind: 'backup_now', label: 'Backup', requires_primary: true, schedulable: true,
      args: [
        { name: 'target', label: 'What to save', type: 'enum', required: true, options: ['all', 'vault', 'config'] },
        { name: 'retention', label: 'Keep the last N', type: 'number' },
      ] },
    { kind: 'shell', label: 'System command', requires_primary: true, schedulable: true,
      args: [
        { name: 'cmd', label: 'Binary', type: 'string', required: true },
        { name: 'args', label: 'Arguments', type: 'string_list' },
      ] },
  ],
  schedForm: { open: false, j: { kind: '', name: '', schedule: '' }, args: {} },
};
comp.schedDescriptor   = new Function('kind', extract('schedDescriptor', 'kind')).bind(comp);
comp.schedArgsOf       = new Function('kind', extract('schedArgsOf', 'kind')).bind(comp);
comp.schedArgDefaults  = new Function('desc', extract('schedArgDefaults', 'desc')).bind(comp);
comp.schedBuildArgs    = new Function('kind', 'argsObj', extract('schedBuildArgs', 'kind, argsObj')).bind(comp);
comp.schedFormArgs     = new Function(extract('schedFormArgs', '')).bind(comp);
comp.schedFormMissing  = new Function(extract('schedFormMissing', '')).bind(comp);

let failed = 0;
const eq = (a, b) => JSON.stringify(a) === JSON.stringify(b);
function check(name, cond) {
  if (cond) { console.log('  ✓', name); } else { console.error('  ✗', name); failed++; }
}

check('enum default = first option', eq(comp.schedArgDefaults(comp.schedDescriptor('backup_now')), { target: 'all', retention: '' }), comp.schedArgDefaults(comp.schedDescriptor('backup_now')));
check('no-arg kind defaults to {}', eq(comp.schedArgDefaults(comp.schedDescriptor('apt_upgrade')), {}));

comp.schedForm = { j: { kind: 'docker_pull', name: 'x', schedule: '0 3 * * *' }, args: { ref: 'nginx:latest' } };
check('docker_pull args = {ref}', eq(comp.schedFormArgs(), { ref: 'nginx:latest' }));

comp.schedForm = { j: { kind: 'docker_compose_pull', name: 'x', schedule: '0 3 * * *' }, args: { dir: '/opt/app' } };
check('compose_pull args = {dir} (folder type)', eq(comp.schedFormArgs(), { dir: '/opt/app' }), comp.schedFormArgs());

comp.schedForm = { j: { kind: 'apt_upgrade', name: 'x', schedule: '0 3 * * *' }, args: {} };
check('apt_upgrade args = {}', eq(comp.schedFormArgs(), {}));

comp.schedForm = { j: { kind: 'shell', name: 'x', schedule: '* * * * *' }, args: { cmd: '/usr/bin/systemctl', args: 'restart\n  \n  nginx  \n' } };
check('shell cmd preserved', comp.schedFormArgs().cmd === '/usr/bin/systemctl');
check('string_list trims + drops blanks', eq(comp.schedFormArgs().args, ['restart', 'nginx']));

comp.schedForm = { j: { kind: 'docker_pull', name: '', schedule: '0 3 * * *' }, args: { ref: '' } };
check('missing name + required ref flagged', comp.schedFormMissing().includes('name') && comp.schedFormMissing().includes('Image'));

comp.schedForm = { j: { kind: 'docker_pull', name: 'ok', schedule: '0 3 * * *' }, args: { ref: 'nginx' } };
check('valid docker_pull → nothing missing', comp.schedFormMissing().length === 0);

comp.schedForm = { j: { kind: 'shell', name: 'ok', schedule: '* * * * *' }, args: { cmd: '', args: '' } };
check('shell missing required cmd flagged', comp.schedFormMissing().includes('Binary'));

comp.schedForm = { j: { kind: 'shell', name: 'ok', schedule: '* * * * *' }, args: { cmd: '/bin/x', args: '' } };
check('shell optional string_list not required', comp.schedFormMissing().length === 0);

check('unknown kind → empty args list', eq(comp.schedArgsOf('does_not_exist'), []));

comp.schedForm = { j: { kind: 'backup_now', name: 'b', schedule: '0 3 * * *' }, args: { target: 'all', retention: '7' }, dest: { type: 'local', localPath: '/opt/backups' } };
check('backup local dest merge', eq(comp.schedFormArgs(), { target: 'all', retention: 7, dest_type: 'local', dest: '/opt/backups' }), comp.schedFormArgs());
comp.schedForm = { j: { kind: 'backup_now', name: 'b', schedule: '0 3 * * *' }, args: { target: 'vault', retention: '' }, dest: { type: 'rclone', remote: 'gdrive', remotePath: 'backups/panel' } };
check('backup rclone dest merge', eq(comp.schedFormArgs(), { target: 'vault', retention: 0, dest_type: 'rclone', remote: 'gdrive', remote_path: 'backups/panel' }), comp.schedFormArgs());

console.log(failed === 0 ? '\nPASS — sched catalogue form logic' : `\nFAIL — ${failed} case(s)`);
process.exit(failed === 0 ? 0 : 1);
