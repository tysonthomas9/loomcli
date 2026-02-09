/**
 * Unit tests for ANSI escape sequence stripping utility.
 */

import { describe, it, expect } from 'vitest';

import { stripAnsi, isEmptyAfterStrip } from '../ansiStrip';

describe('stripAnsi', () => {
  it('returns plain text unchanged', () => {
    expect(stripAnsi('Hello World')).toBe('Hello World');
  });

  it('returns empty string unchanged', () => {
    expect(stripAnsi('')).toBe('');
  });

  it('strips basic SGR color codes', () => {
    expect(stripAnsi('\x1b[31mError\x1b[0m')).toBe('Error');
  });

  it('strips bold and underline codes', () => {
    expect(stripAnsi('\x1b[1m\x1b[4mBold Underline\x1b[0m')).toBe('Bold Underline');
  });

  it('strips 256-color and truecolor codes', () => {
    expect(stripAnsi('\x1b[38;5;196mRed\x1b[0m')).toBe('Red');
    expect(stripAnsi('\x1b[38;2;255;0;0mRed\x1b[0m')).toBe('Red');
  });

  it('converts cursor-forward with count to spaces', () => {
    expect(stripAnsi('\x1b[3Cfoo')).toBe('   foo');
  });

  it('converts cursor-forward without count to single space', () => {
    expect(stripAnsi('\x1b[Cfoo')).toBe(' foo');
  });

  it('converts cursor-forward between words to spaces', () => {
    expect(stripAnsi('Claude\x1b[1CCode')).toBe('Claude Code');
  });

  it('handles multiple cursor-forward sequences', () => {
    expect(stripAnsi('\x1b[4Cfoo\x1b[2Cbar')).toBe('    foo  bar');
  });

  it('strips cursor-back/up/down/position sequences', () => {
    expect(stripAnsi('\x1b[2Dfoo')).toBe('foo');
    expect(stripAnsi('\x1b[6Afoo')).toBe('foo');
    expect(stripAnsi('\x1b[3Bfoo')).toBe('foo');
    expect(stripAnsi('\x1b[10;20Hfoo')).toBe('foo');
  });

  it('strips OSC title-setting sequences (BEL terminated)', () => {
    expect(stripAnsi('\x1b]0;Window Title\x07text')).toBe('text');
  });

  it('strips OSC sequences (ST terminated)', () => {
    expect(stripAnsi('\x1b]0;Window Title\x1b\\text')).toBe('text');
  });

  it('strips terminal mode sequences', () => {
    expect(stripAnsi('\x1b[?2004h')).toBe('');
    expect(stripAnsi('\x1b[?2004l')).toBe('');
    expect(stripAnsi('\x1b[?25l')).toBe('');
    expect(stripAnsi('\x1b[?1004h')).toBe('');
  });

  it('strips bracket paste mode sequences', () => {
    expect(stripAnsi('\x1b[?2026l \x1b[?2026h')).toBe(' ');
  });

  it('handles mixed content: real text + escape sequences', () => {
    const input = '\x1b[?2004h\x1b[1mClaude\x1b[1CCode\x1b[0m\x1b[1Cv2.1.37';
    expect(stripAnsi(input)).toBe('Claude Code v2.1.37');
  });

  it('handles real-world garbled log line', () => {
    const input = '[?2026l [?2026h [6A';
    // These are literal bracket sequences (not real ESC codes) - they stay as-is
    // since they don't start with \x1b
    expect(stripAnsi(input)).toBe('[?2026l [?2026h [6A');
  });

  it('handles real ESC-prefixed garbled line', () => {
    const input = '\x1b[?2026l \x1b[?2026h \x1b[6A';
    expect(stripAnsi(input)).toBe('  ');
  });

  it('strips bare ESC characters at end of string', () => {
    expect(stripAnsi('foo\x1b')).toBe('foo');
  });

  it('strips single-character escape sequences', () => {
    expect(stripAnsi('\x1b=foo\x1b>')).toBe('foo');
  });

  it('preserves unicode content', () => {
    expect(stripAnsi('Hello World \u2713 \u2717')).toBe('Hello World \u2713 \u2717');
  });

  it('preserves box-drawing characters', () => {
    expect(stripAnsi('\u250c\u2500\u2500\u2510')).toBe('\u250c\u2500\u2500\u2510');
  });

  it('handles string with only ESC characters', () => {
    expect(stripAnsi('\x1b\x1b\x1b')).toBe('');
  });
});

describe('isEmptyAfterStrip', () => {
  it('returns true for empty string', () => {
    expect(isEmptyAfterStrip('')).toBe(true);
  });

  it('returns true for whitespace-only string', () => {
    expect(isEmptyAfterStrip('   ')).toBe(true);
  });

  it('returns true for line with only escape sequences', () => {
    expect(isEmptyAfterStrip('\x1b[?2004h\x1b[?1004h\x1b[?25l')).toBe(true);
  });

  it('returns true for line with escape sequences and whitespace', () => {
    expect(isEmptyAfterStrip('\x1b[?2004h  \x1b[?1004h  ')).toBe(true);
  });

  it('returns false for line with text content', () => {
    expect(isEmptyAfterStrip('Hello')).toBe(false);
  });

  it('returns false for line with text and escape sequences', () => {
    expect(isEmptyAfterStrip('\x1b[31mError\x1b[0m')).toBe(false);
  });

  it('returns true for cursor-forward-only line (spaces after strip)', () => {
    expect(isEmptyAfterStrip('\x1b[10C')).toBe(true);
  });
});
