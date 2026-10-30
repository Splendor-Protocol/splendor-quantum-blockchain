import { describe, expect, it } from 'vitest';
import { encodeQr, qrInternals, qrSvgPath } from '../widgets/parts/qr.js';

/** "HELLO", auto mask, boosted to ECC H — decoded by OpenCV when this vector was recorded. */
const HELLO_V1 = [
  '#######..#..#.#######',
  '#.....#.###...#.....#',
  '#.###.#..#.##.#.###.#',
  '#.###.#..###..#.###.#',
  '#.###.#..##.#.#.###.#',
  '#.....#.###...#.....#',
  '#######.#.#.#.#######',
  '........###.#........',
  '....####.#..#.##...#.',
  '#.###..###...###.####',
  '##..####.##.#..##..#.',
  '.###.#..#.###.#.#....',
  '####..######......##.',
  '........###......#.##',
  '#######.#...###..#.#.',
  '#.....#.####.###...#.',
  '#.###.#.#..#...##.#.#',
  '#.###.#..#.#..#..#.##',
  '#.###.#..#.#.#.###...',
  '#.....#....#.##......',
  '#######..#.##..##.#.#',
];

const rows = (m: boolean[][]) => m.map((r) => r.map((c) => (c ? '#' : '.')).join(''));

/** Read the 15 format bits around the top-left finder and return (ecc bits, mask). */
function readFormat(m: boolean[][]): { ecc: number; mask: number } {
  let bits = 0;
  const at = (x: number, y: number) => (m[y]![x] ? 1 : 0);
  const pos: Array<[number, number]> = [];
  for (let i = 0; i <= 5; i++) pos.push([8, i]);
  pos.push([8, 7], [8, 8], [7, 8]);
  for (let i = 9; i < 15; i++) pos.push([14 - i, 8]);
  pos.forEach(([x, y], i) => (bits |= at(x, y) << i));
  const data = (bits ^ 0x5412) >>> 10;
  return { ecc: data >>> 3, mask: data & 7 };
}

describe('QR encoder', () => {
  it('matches the ISO/IEC 18004 Annex I Reed-Solomon example (1-M, "01234567")', () => {
    const data = [0x10, 0x20, 0x0c, 0x56, 0x61, 0x80, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11];
    const ecc = qrInternals.rsRemainder(data, qrInternals.rsDivisor(10));
    expect(ecc).toEqual([0xa5, 0x24, 0xd4, 0xc1, 0xed, 0x36, 0xc7, 0x87, 0x2c, 0x55]);
  });

  it('has the standard data capacities', () => {
    expect(qrInternals.numRawDataModules(1)).toBe(208);
    expect(qrInternals.numDataCodewords(1, 'L')).toBe(19);
    expect(qrInternals.numDataCodewords(1, 'H')).toBe(9);
    expect(qrInternals.numDataCodewords(10, 'M')).toBe(216);
    expect(qrInternals.numDataCodewords(40, 'L')).toBe(2956);
    expect(qrInternals.numDataCodewords(40, 'H')).toBe(1276);
  });

  it('reproduces a recorded version-1 vector', () => {
    const qr = encodeQr('HELLO', { ecc: 'L' });
    expect(qr.version).toBe(1);
    expect(rows(qr.modules)).toEqual(HELLO_V1);
    expect(readFormat(qr.modules).ecc).toBe(2);
  });

  it('picks the smallest version and writes finder, timing and format patterns', () => {
    const addr = 'SQ:' + 'ab'.repeat(32);
    const qr = encodeQr(addr, { ecc: 'M', boostEcc: false });
    expect(qr.version).toBe(5);
    expect(qr.size).toBe(37);
    const r = rows(qr.modules);
    for (const [x, y] of [[0, 0], [qr.size - 7, 0], [0, qr.size - 7]] as const) {
      expect(r[y]!.slice(x, x + 7)).toBe('#######');
      expect(r[y + 2]!.slice(x, x + 7)).toBe('#.###.#');
    }
    for (let i = 8; i < qr.size - 8; i++) expect(qr.modules[6]![i]).toBe(i % 2 === 0);
    expect(qr.modules[qr.size - 8]![8]).toBe(true);
    expect(readFormat(qr.modules).ecc).toBe(0);
  });

  it('uses the requested mask, encodes UTF-8, and rejects oversize input', () => {
    for (let mask = 0; mask < 8; mask++) expect(readFormat(encodeQr('mask', { mask }).modules).mask).toBe(mask);
    expect(encodeQr('é漢字').version).toBe(1);
    expect(encodeQr('x'.repeat(2953), { ecc: 'L' }).version).toBe(40);
    expect(() => encodeQr('x'.repeat(2954), { ecc: 'L' })).toThrow(RangeError);
    expect(() => encodeQr('x', { mask: 9 })).toThrow(RangeError);
  });

  it('renders an SVG path with a quiet-zone offset', () => {
    const qr = encodeQr('A', { ecc: 'L' });
    const path = qrSvgPath(qr, 4);
    expect(path.startsWith('M4,4h1v1h-1z')).toBe(true);
    expect(path.split('z').length - 1).toBe(qr.modules.flat().filter(Boolean).length);
  });
});
