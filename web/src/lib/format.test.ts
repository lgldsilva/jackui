import { describe, expect, it } from 'vitest'
import {
  formatBytes,
  formatRate,
  formatDuration,
  bytesUnit,
  formatBytesAs,
  formatBytesPair,
} from './format'

describe('bytesUnit', () => {
  it('should return 0 for zero or negative values', () => {
    expect(bytesUnit(0)).toBe(0)
    expect(bytesUnit(-10)).toBe(0)
  })

  it('should return correct unit index', () => {
    expect(bytesUnit(500)).toBe(0) // B
    expect(bytesUnit(1024)).toBe(1) // KB
    expect(bytesUnit(1.5 * 1024 * 1024)).toBe(2) // MB
    expect(bytesUnit(10 * 1024 * 1024 * 1024)).toBe(3) // GB
    expect(bytesUnit(1024 * 1024 * 1024 * 1024 * 1024)).toBe(4) // TB (maximum)
  })
})

describe('formatBytesAs', () => {
  it('should format bytes with specified unit index', () => {
    // 0: B, 1: KB, 2: MB, 3: GB
    expect(formatBytesAs(1500, 0)).toBe('1500 B')
    expect(formatBytesAs(1500, 1)).toBe('1.46 KB')
    expect(formatBytesAs(1024 * 1024, 2)).toBe('1 MB')
    expect(formatBytesAs(1.55 * 1024 * 1024, 2)).toBe('1.55 MB')
    expect(formatBytesAs(0, 2)).toBe('0 MB')
    expect(formatBytesAs(-50, 3)).toBe('0 GB')
  })
})

describe('formatBytes', () => {
  it('should format bytes dynamically', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(500)).toBe('500 B')
    expect(formatBytes(1024)).toBe('1 KB')
    expect(formatBytes(1500)).toBe('1.46 KB')
    expect(formatBytes(1024 * 1024 * 1.5)).toBe('1.5 MB')
  })
})

describe('formatRate', () => {
  it('should format rate correctly', () => {
    expect(formatRate(0)).toBe('0 KB/s')
    expect(formatRate(1024)).toBe('1 KB/s')
    expect(formatRate(1.5 * 1024 * 1024)).toBe('1.5 MB/s')
  })
})

describe('formatDuration', () => {
  it('should format duration in h:mm:ss or m:ss', () => {
    expect(formatDuration(0)).toBe('0:00')
    expect(formatDuration(-10)).toBe('0:00')
    expect(formatDuration(45)).toBe('0:45')
    expect(formatDuration(60)).toBe('1:00')
    expect(formatDuration(3600)).toBe('1:00:00')
    expect(formatDuration(3665)).toBe('1:01:05')
  })
 })

describe('formatBytesPair', () => {
  it('should format two values with the unit of the second value', () => {
    // 999.5 MB / 1.2 GB
    expect(formatBytesPair(999.5 * 1024 * 1024, 1.2 * 1024 * 1024 * 1024)).toBe('0.98 GB / 1.2 GB')
    // 50 KB / 500 KB
    expect(formatBytesPair(50 * 1024, 500 * 1024)).toBe('50 KB / 500 KB')
    // 0 / 10 MB
    expect(formatBytesPair(0, 10 * 1024 * 1024)).toBe('0 MB / 10 MB')
  })
})
