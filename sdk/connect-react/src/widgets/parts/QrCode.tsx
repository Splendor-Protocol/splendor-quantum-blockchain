import { useMemo } from 'react';
import { encodeQr, qrSvgPath, type QrEcc } from './qr.js';

export type QrCodeProps = {
  /** Text to encode (UTF-8). */
  value: string;
  /** Rendered width and height in px. Default 176. */
  size?: number;
  /** Error-correction level. Default "M". */
  ecc?: QrEcc;
  /** Accessible name for the image. */
  label?: string;
  className?: string;
};

/**
 * QR code as an inline SVG. Always dark modules on a light square with the standard
 * 4-module quiet zone, whatever the theme, so phone cameras can read it.
 */
export function QrCode({ value, size = 176, ecc = 'M', label, className }: QrCodeProps) {
  const qr = useMemo(() => {
    try {
      return encodeQr(value, { ecc });
    } catch {
      return null;
    }
  }, [value, ecc]);
  if (!qr) return null;
  const border = 4;
  const dim = qr.size + border * 2;
  return (
    <svg
      className={['sqc-qr', className].filter(Boolean).join(' ')}
      role="img"
      aria-label={label ?? value}
      width={size}
      height={size}
      viewBox={`0 0 ${dim} ${dim}`}
      shapeRendering="crispEdges"
      data-qr-version={qr.version}
    >
      <rect width={dim} height={dim} fill="#ffffff" />
      <path d={qrSvgPath(qr, border)} fill="#000000" />
    </svg>
  );
}
