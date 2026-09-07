import { describe, expect, it } from 'vitest';
import { MODULE_ROUTER_ADDRESS, deriveModuleRouterAddress, qrc20Address } from '../addresses/index.js';
import { buildModuleCall, encodeModuleCallHex } from '../core/module-call.js';

describe('addresses', () => {
  it('module router matches golden vector', () => {
    expect(deriveModuleRouterAddress()).toBe(MODULE_ROUTER_ADDRESS);
    expect(MODULE_ROUTER_ADDRESS).toBe(
      'SQ:b07043f4ef749951893985d66494d0f4e44c8812053361c07ea24bc70a444dcd',
    );
  });

  it('qrc20 address is deterministic', () => {
    const deployer = 'SQ:307f4559431e680933fc1c62503f050140e5740068248d7374461e0311a2728e';
    const a0 = qrc20Address(deployer, 0);
    const a1 = qrc20Address(deployer, 1);
    expect(a0).toMatch(/^SQ:[0-9a-f]{64}$/);
    expect(a1).not.toBe(a0);
  });
});

describe('module-call', () => {
  it('builds qrc20 transfer envelope', () => {
    const env = buildModuleCall('qrc20', 'transfer', {
      token: 'SQ:' + 'a'.repeat(64),
      to: 'SQ:' + 'b'.repeat(64),
      amount: '1000',
    });
    expect(env.module).toBe('qrc20');
    expect(env.action).toBe('transfer');
    const hex = encodeModuleCallHex(env);
    expect(hex.startsWith('0x')).toBe(true);
    const parsed = JSON.parse(Buffer.from(hex.slice(2), 'hex').toString('utf8'));
    expect(parsed.action).toBe('transfer');
  });
});
