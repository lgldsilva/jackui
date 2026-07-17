// CP437 (IBM PC / DOS) decoding for scene NFO files. TextDecoder has no
// 'cp437' label, and decoding NFO ASCII art as UTF-8/latin1 turns the box
// drawing (─║╔╗ ░▒▓█) into mojibake — the whole point of an NFO viewer.

// Glyphs for bytes 0x80–0xFF, exactly 128 chars, straight from the CP437 table.
const CP437_HIGH =
  'ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»' +
  '░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀' +
  'αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ '

export function decodeCP437(bytes: Uint8Array): string {
  let out = ''
  for (const b of bytes) {
    out += b < 0x80 ? String.fromCodePoint(b) : CP437_HIGH[b - 0x80]
  }
  return out
}

// decodeText decodes file bytes for the text viewer: strict UTF-8 first
// (covers modern files), CP437 on failure when the file smells like an NFO,
// lenient UTF-8 (U+FFFD replacement) otherwise.
export function decodeText(buf: ArrayBuffer, preferCp437: boolean): string {
  const bytes = new Uint8Array(buf)
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    if (preferCp437) return decodeCP437(bytes)
    return new TextDecoder('utf-8', { fatal: false }).decode(bytes)
  }
}
