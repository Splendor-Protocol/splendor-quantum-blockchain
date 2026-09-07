import { describe, expect, it } from 'vitest';
import { bytecodeContractAddress } from '../addresses/index.js';
import { Contract } from '../contracts/contract.js';
import { X402Client } from '../x402/client.js';
import { Connection } from '../core/connection.js';
import { fromSPLD, toSPLD } from '../rpc.js';
import { buildModuleCall, encodeModuleCallHex } from '../core/module-call.js';

describe('Phase 2 — contracts', () => {
  it('bytecode contract address is deterministic', () => {
    const deployer = 'SQ:307f4559431e680933fc1c62503f050140e5740068248d7374461e0311a2728e';
    const a0 = bytecodeContractAddress(deployer, 0);
    const a1 = Contract.predictAddress(deployer, 1);
    expect(a0).toMatch(/^SQ:[0-9a-f]{64}$/);
    expect(a1).not.toBe(a0);
  });
});

describe('Phase 2 — x402', () => {
  it('buildRequirements converts SPLD to units', () => {
    const conn = new Connection({ rpcUrl: 'http://127.0.0.1:8080', chainId: 'splendor-quantum-1' });
    const client = X402Client.fromConnection(conn);
    const req = client.buildRequirements(
      'SQ:' + 'a'.repeat(64),
      '1.0',
    );
    expect(req.amount).toBe(fromSPLD('1.0'));
    expect(toSPLD(req.amount)).toContain('1');
  });
});

describe('Phase 2 — module-call encoding', () => {
  it('encodes deploy envelope for estimate', () => {
    const env = buildModuleCall('qrc20', 'deploy', {
      name: 'T',
      symbol: 'T',
      decimals: 18,
      total_supply: '1000',
    });
    const hex = encodeModuleCallHex(env);
    expect(hex.length).toBeGreaterThan(10);
  });
});
