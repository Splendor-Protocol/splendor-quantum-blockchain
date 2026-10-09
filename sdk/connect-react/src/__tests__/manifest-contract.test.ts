import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import * as root from '../index.js';
import { MANIFEST_SCHEMA_VERSION, WIDGET_MANIFESTS, getWidgetManifest } from '../manifest.js';

const widgetsDir = fileURLToPath(new URL('../widgets/', import.meta.url));
const manifestSrc = fileURLToPath(new URL('../manifest.ts', import.meta.url));

describe('widget manifest contract', () => {
  it('has a manifest for every drop-in widget file', () => {
    const widgetFiles = readdirSync(widgetsDir).filter((f) => /^[A-Z]\w+\.tsx$/.test(f));
    expect(widgetFiles.length).toBeGreaterThan(0);
    const names = new Set(WIDGET_MANIFESTS.map((m) => m.importName));
    for (const file of widgetFiles) {
      expect(names, `missing manifest for ${file}`).toContain(file.replace(/\.tsx$/, ''));
    }
  });

  it('every manifest names a real root export', () => {
    for (const m of WIDGET_MANIFESTS) {
      expect(typeof (root as Record<string, unknown>)[m.importName], m.importName).toBe('function');
      expect(m.package).toBe('@splendor/connect-react');
    }
  });

  it('ids are unique and defaults only use declared props', () => {
    const ids = WIDGET_MANIFESTS.map((m) => m.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const m of WIDGET_MANIFESTS) {
      expect(m.id).toMatch(/^[a-z][a-z0-9-]*$/);
      const declared = new Set(m.props.map((p) => p.name));
      for (const key of Object.keys(m.defaults)) expect(declared, `${m.id}.${key}`).toContain(key);
      for (const p of m.props) {
        if (p.type === 'select') expect(p.options?.length, `${m.id}.${p.name}`).toBeGreaterThan(0);
      }
      expect(m.since).toMatch(/^\d+\.\d+\.\d+$/);
    }
    expect(getWidgetManifest('transfer')?.importName).toBe('TransferWidget');
    expect(MANIFEST_SCHEMA_VERSION).toBe(1);
  });

  it('stays data-only (no imports, so loading it has no side effects)', () => {
    const src = readFileSync(manifestSrc, 'utf8');
    expect(src).not.toMatch(/^\s*import\s/m);
  });
});
