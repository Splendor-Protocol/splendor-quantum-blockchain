/**
 * Native module call envelope — matches vm.ModuleCall JSON wire format.
 */
export type ModuleCall = {
  module: string;
  action: string;
  params: Record<string, unknown>;
};

export function buildModuleCall(
  module: string,
  action: string,
  params: Record<string, unknown>,
): ModuleCall {
  return { module, action, params };
}

export function encodeModuleCall(envelope: ModuleCall): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(envelope));
}

export function encodeModuleCallHex(envelope: ModuleCall): string {
  const bytes = encodeModuleCall(envelope);
  return (
    '0x' +
    Array.from(bytes)
      .map((b) => b.toString(16).padStart(2, '0'))
      .join('')
  );
}
