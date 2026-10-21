export type SpacerProps = { size?: string };

/** Vertical breathing room between pieces. */
export function Spacer({ size = '1.5rem' }: SpacerProps) {
  return <div className="dapp-spacer" data-sqc-widget="spacer" style={{ height: size }} aria-hidden="true" />;
}
