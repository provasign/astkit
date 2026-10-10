// TypeScript sample with 'apostrophe
interface Shape {
  name: string; // don't
}

type Tpl = `prefix-${string}`;

export function area<T extends Shape>(s: T, w: number, h: number): number {
  const label = `shape ${s.name}: ${w * h}`;
  const parts = s.name.split(/[,;]/);
  const half = w / 2;
  return half * h + parts.length + label.length;
}

/** Doc: class Phantom {} */
export class Box implements Shape {
  #secret = 'x';
  name = "box";
}
