import { beforeEach, describe, expect, it } from 'vitest';
import { emptySpec } from '@splendor/dapp-spec';
import {
  clearLocalProjects,
  createLocalWorkspace,
  deleteLocalProject,
  deleteLocalWorkspace,
  getLocalProject,
  isLocalWorkspaceId,
  listLocalProjects,
  saveLocalProject,
} from './local';

beforeEach(() => localStorage.clear());

describe('local projects keyed by workspace', () => {
  it('creates, updates and lists per workspace, newest first', async () => {
    const a = saveLocalProject('local-a', emptySpec('First'));
    await new Promise((r) => setTimeout(r, 5));
    const b = saveLocalProject('local-a', emptySpec('Second'));
    saveLocalProject('local-b', emptySpec('Other workspace'));
    expect(listLocalProjects('local-a').map((p) => p.name)).toEqual(['Second', 'First']);
    expect(listLocalProjects('local-b').map((p) => p.name)).toEqual(['Other workspace']);
    expect(a.id).not.toBe(b.id);

    await new Promise((r) => setTimeout(r, 5));
    const renamed = saveLocalProject('local-a', emptySpec('First v2'), a.id);
    expect(renamed.id).toBe(a.id);
    expect(renamed.createdAt).toBe(a.createdAt);
    expect(listLocalProjects('local-a').map((p) => p.name)).toEqual(['First v2', 'Second']);
    expect(getLocalProject('local-a', a.id)?.spec.name).toBe('First v2');
    expect(getLocalProject('local-b', a.id)).toBeNull();
  });

  it('deletes projects, and drops them with their workspace', () => {
    const ws = createLocalWorkspace('Mine');
    expect(isLocalWorkspaceId(ws.id)).toBe(true);
    const p = saveLocalProject(ws.id, emptySpec('Keep'));
    saveLocalProject(ws.id, emptySpec('Drop'));
    expect(deleteLocalProject(ws.id, p.id)).toBe(true);
    expect(deleteLocalProject(ws.id, p.id)).toBe(false);
    expect(listLocalProjects(ws.id)).toHaveLength(1);
    expect(deleteLocalWorkspace(ws.id)).toBe(true);
    expect(listLocalProjects(ws.id)).toEqual([]);
    clearLocalProjects('never-existed');
  });

  it('survives corrupt storage', () => {
    localStorage.setItem('sqc-playground-workspace-projects-v1', '{not json');
    expect(listLocalProjects('local-a')).toEqual([]);
    localStorage.setItem('sqc-playground-workspace-projects-v1', JSON.stringify({ 'local-a': 'nope', 'local-b': [{ id: 1 }, null] }));
    expect(listLocalProjects('local-a')).toEqual([]);
    expect(listLocalProjects('local-b')).toEqual([]);
    expect(saveLocalProject('local-a', emptySpec('Fresh')).name).toBe('Fresh');
  });

  it('only treats local- ids as local', () => {
    expect(isLocalWorkspaceId('local-x')).toBe(true);
    expect(isLocalWorkspaceId('3f2a')).toBe(false);
    expect(isLocalWorkspaceId(null)).toBe(false);
  });
});
