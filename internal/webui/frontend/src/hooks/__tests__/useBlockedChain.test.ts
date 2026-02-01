/**
 * @vitest-environment jsdom
 */
import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import {
  useBlockedChain,
  getBlockedChain,
  computeAllBlockedCounts,
} from '../useBlockedChain'
import type { DependencyEdge } from '@/types'

/**
 * Helper to create a mock DependencyEdge for testing.
 *
 * Edge semantics:
 * - sourceIssueId = the issue that has a dependency (is blocked)
 * - targetIssueId = the issue being depended upon (the blocker)
 *
 * So if A blocks B:
 *   createBlockingEdge('A', 'B') creates an edge where:
 *   - sourceIssueId = 'B' (the blocked issue)
 *   - targetIssueId = 'A' (the blocker)
 */
function createBlockingEdge(
  blockerId: string,
  blockedId: string,
  isBlocking: boolean = true
): DependencyEdge {
  // sourceIssueId = the issue that has the dependency (blocked issue)
  // targetIssueId = the issue being depended upon (blocker)
  return {
    id: `${blockedId}-depends-on-${blockerId}`,
    source: blockedId,
    target: blockerId,
    type: 'dependency',
    data: {
      dependencyType: isBlocking ? 'blocks' : 'related',
      isBlocking,
      sourceIssueId: blockedId,
      targetIssueId: blockerId,
    },
  }
}

/**
 * Test graph structure (using blocking relationships):
 *
 * Simple chain: A -> B -> C
 *   - A blocks B, B blocks C
 *   - C's blockers: [B, A]
 *   - A's blockedBy (downstream): [B, C]
 *
 * Diamond pattern: D -> E, D -> F, E -> G, F -> G
 *   - D blocks E and F
 *   - E and F both block G
 *   - G's blockers: [E, F, D]
 *   - D's blockedBy (downstream): [E, F, G]
 *
 * Circular: H -> I -> J -> H
 *   - Should not infinite loop
 */
const simpleChainEdges: DependencyEdge[] = [
  createBlockingEdge('A', 'B'), // A blocks B
  createBlockingEdge('B', 'C'), // B blocks C
]

const diamondEdges: DependencyEdge[] = [
  createBlockingEdge('D', 'E'), // D blocks E
  createBlockingEdge('D', 'F'), // D blocks F
  createBlockingEdge('E', 'G'), // E blocks G
  createBlockingEdge('F', 'G'), // F blocks G
]

const circularEdges: DependencyEdge[] = [
  createBlockingEdge('H', 'I'), // H blocks I
  createBlockingEdge('I', 'J'), // I blocks J
  createBlockingEdge('J', 'H'), // J blocks H (creates cycle)
]

const mixedEdges: DependencyEdge[] = [
  createBlockingEdge('M1', 'M2', true), // M1 blocks M2 (blocking)
  createBlockingEdge('M2', 'M3', false), // M2 -> M3 (non-blocking, related)
  createBlockingEdge('M3', 'M4', true), // M3 blocks M4 (blocking)
]

describe('getBlockedChain', () => {
  describe('simple chain', () => {
    it('returns empty blockers for root blocker (A)', () => {
      const result = getBlockedChain('A', simpleChainEdges)
      expect(result.blockers.size).toBe(0)
    })

    it('returns A as blocker for B', () => {
      const result = getBlockedChain('B', simpleChainEdges)
      expect(result.blockers.has('A')).toBe(true)
      expect(result.blockers.size).toBe(1)
    })

    it('returns A and B as transitive blockers for C', () => {
      const result = getBlockedChain('C', simpleChainEdges)
      expect(result.blockers.has('A')).toBe(true)
      expect(result.blockers.has('B')).toBe(true)
      expect(result.blockers.size).toBe(2)
    })

    it('returns B and C as blocked by A', () => {
      const result = getBlockedChain('A', simpleChainEdges)
      expect(result.blockedBy.has('B')).toBe(true)
      expect(result.blockedBy.has('C')).toBe(true)
      expect(result.blockedBy.size).toBe(2)
    })

    it('returns only C as blocked by B', () => {
      const result = getBlockedChain('B', simpleChainEdges)
      expect(result.blockedBy.has('C')).toBe(true)
      expect(result.blockedBy.size).toBe(1)
    })

    it('returns empty blockedBy for leaf node C', () => {
      const result = getBlockedChain('C', simpleChainEdges)
      expect(result.blockedBy.size).toBe(0)
    })

    it('returns correct blockedCount', () => {
      const resultA = getBlockedChain('A', simpleChainEdges)
      const resultB = getBlockedChain('B', simpleChainEdges)
      const resultC = getBlockedChain('C', simpleChainEdges)

      expect(resultA.blockedCount).toBe(2)
      expect(resultB.blockedCount).toBe(1)
      expect(resultC.blockedCount).toBe(0)
    })
  })

  describe('diamond pattern', () => {
    it('returns D, E, F as blockers for G', () => {
      const result = getBlockedChain('G', diamondEdges)
      expect(result.blockers.has('D')).toBe(true)
      expect(result.blockers.has('E')).toBe(true)
      expect(result.blockers.has('F')).toBe(true)
      expect(result.blockers.size).toBe(3)
    })

    it('returns E, F, G as blocked by D', () => {
      const result = getBlockedChain('D', diamondEdges)
      expect(result.blockedBy.has('E')).toBe(true)
      expect(result.blockedBy.has('F')).toBe(true)
      expect(result.blockedBy.has('G')).toBe(true)
      expect(result.blockedBy.size).toBe(3)
    })

    it('returns only G as blocked by E', () => {
      const result = getBlockedChain('E', diamondEdges)
      expect(result.blockedBy.has('G')).toBe(true)
      expect(result.blockedBy.size).toBe(1)
    })

    it('returns only D as blocker for E', () => {
      const result = getBlockedChain('E', diamondEdges)
      expect(result.blockers.has('D')).toBe(true)
      expect(result.blockers.size).toBe(1)
    })
  })

  describe('circular dependencies', () => {
    it('does not infinite loop with circular edges for H', () => {
      const result = getBlockedChain('H', circularEdges)
      // Should complete without infinite loop
      expect(result.blockers.size).toBeLessThanOrEqual(3)
      expect(result.blockedBy.size).toBeLessThanOrEqual(3)
    })

    it('does not infinite loop with circular edges for I', () => {
      const result = getBlockedChain('I', circularEdges)
      expect(result.blockers.size).toBeLessThanOrEqual(3)
      expect(result.blockedBy.size).toBeLessThanOrEqual(3)
    })

    it('does not infinite loop with circular edges for J', () => {
      const result = getBlockedChain('J', circularEdges)
      expect(result.blockers.size).toBeLessThanOrEqual(3)
      expect(result.blockedBy.size).toBeLessThanOrEqual(3)
    })
  })

  describe('mixed blocking and non-blocking edges', () => {
    it('only follows blocking edges for blockers', () => {
      const result = getBlockedChain('M4', mixedEdges)
      // M4 <- M3 (blocking), M3 <- M2 (non-blocking), so only M3 is a blocker
      expect(result.blockers.has('M3')).toBe(true)
      expect(result.blockers.has('M2')).toBe(false)
      expect(result.blockers.has('M1')).toBe(false)
      expect(result.blockers.size).toBe(1)
    })

    it('only follows blocking edges for blockedBy', () => {
      const result = getBlockedChain('M1', mixedEdges)
      // M1 -> M2 (blocking), M2 -> M3 (non-blocking)
      expect(result.blockedBy.has('M2')).toBe(true)
      expect(result.blockedBy.has('M3')).toBe(false)
      expect(result.blockedBy.has('M4')).toBe(false)
      expect(result.blockedBy.size).toBe(1)
    })
  })

  describe('edge cases', () => {
    it('handles empty edges array', () => {
      const result = getBlockedChain('X', [])
      expect(result.blockers.size).toBe(0)
      expect(result.blockedBy.size).toBe(0)
      expect(result.blockedCount).toBe(0)
    })

    it('handles issue not in any edge', () => {
      const result = getBlockedChain('X', simpleChainEdges)
      expect(result.blockers.size).toBe(0)
      expect(result.blockedBy.size).toBe(0)
      expect(result.blockedCount).toBe(0)
    })

    it('handles edges with missing data', () => {
      const edgesWithMissingData: DependencyEdge[] = [
        {
          id: 'bad-edge',
          source: 'Y',
          target: 'X',
          type: 'dependency',
          data: undefined,
        } as unknown as DependencyEdge,
      ]
      const result = getBlockedChain('Y', edgesWithMissingData)
      // Should not crash
      expect(result.blockers.size).toBe(0)
      expect(result.blockedBy.size).toBe(0)
    })
  })
})

describe('computeAllBlockedCounts', () => {
  it('computes counts for all issues in simple chain', () => {
    const counts = computeAllBlockedCounts(['A', 'B', 'C'], simpleChainEdges)

    expect(counts.get('A')).toBe(2) // A blocks B and C
    expect(counts.get('B')).toBe(1) // B blocks C
    expect(counts.get('C')).toBe(0) // C blocks nothing
  })

  it('computes counts for diamond pattern', () => {
    const counts = computeAllBlockedCounts(
      ['D', 'E', 'F', 'G'],
      diamondEdges
    )

    expect(counts.get('D')).toBe(3) // D blocks E, F, and G
    expect(counts.get('E')).toBe(1) // E blocks G
    expect(counts.get('F')).toBe(1) // F blocks G
    expect(counts.get('G')).toBe(0) // G blocks nothing
  })

  it('handles issues not in any edge', () => {
    const counts = computeAllBlockedCounts(['X', 'Y'], simpleChainEdges)

    expect(counts.get('X')).toBe(0)
    expect(counts.get('Y')).toBe(0)
  })

  it('handles empty issue list', () => {
    const counts = computeAllBlockedCounts([], simpleChainEdges)
    expect(counts.size).toBe(0)
  })

  it('handles empty edges', () => {
    const counts = computeAllBlockedCounts(['A', 'B', 'C'], [])

    expect(counts.get('A')).toBe(0)
    expect(counts.get('B')).toBe(0)
    expect(counts.get('C')).toBe(0)
  })

  it('does not infinite loop with circular edges', () => {
    const counts = computeAllBlockedCounts(['H', 'I', 'J'], circularEdges)

    // Should complete without infinite loop
    expect(counts.size).toBe(3)
    // Each node in the cycle should have some blocked count
    expect(counts.get('H')).toBeLessThanOrEqual(3)
    expect(counts.get('I')).toBeLessThanOrEqual(3)
    expect(counts.get('J')).toBeLessThanOrEqual(3)
  })
})

describe('useBlockedChain', () => {
  describe('getChain', () => {
    it('returns correct result for simple chain', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges })
      )

      const chainA = result.current.getChain('A')
      expect(chainA.blockers.size).toBe(0)
      expect(chainA.blockedBy.size).toBe(2)
      expect(chainA.blockedCount).toBe(2)

      const chainC = result.current.getChain('C')
      expect(chainC.blockers.size).toBe(2)
      expect(chainC.blockedBy.size).toBe(0)
      expect(chainC.blockedCount).toBe(0)
    })

    it('returns correct result for diamond pattern', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: diamondEdges })
      )

      const chainG = result.current.getChain('G')
      expect(chainG.blockers.has('D')).toBe(true)
      expect(chainG.blockers.has('E')).toBe(true)
      expect(chainG.blockers.has('F')).toBe(true)
      expect(chainG.blockedBy.size).toBe(0)
    })
  })

  describe('getChainIds', () => {
    it('returns all IDs in the chain including the issue itself', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges })
      )

      const chainIdsB = result.current.getChainIds('B')
      expect(chainIdsB.has('A')).toBe(true) // blocker
      expect(chainIdsB.has('B')).toBe(true) // self
      expect(chainIdsB.has('C')).toBe(true) // blocked by
      expect(chainIdsB.size).toBe(3)
    })

    it('includes only self for isolated node', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges })
      )

      const chainIdsX = result.current.getChainIds('X')
      expect(chainIdsX.has('X')).toBe(true)
      expect(chainIdsX.size).toBe(1)
    })

    it('includes all connected nodes for diamond pattern', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: diamondEdges })
      )

      const chainIdsE = result.current.getChainIds('E')
      expect(chainIdsE.has('D')).toBe(true) // blocker
      expect(chainIdsE.has('E')).toBe(true) // self
      expect(chainIdsE.has('G')).toBe(true) // blocked by
      expect(chainIdsE.size).toBe(3)
    })
  })

  describe('empty edges array', () => {
    it('returns empty result for any issue', () => {
      const { result } = renderHook(() => useBlockedChain({ edges: [] }))

      const chain = result.current.getChain('X')
      expect(chain.blockers.size).toBe(0)
      expect(chain.blockedBy.size).toBe(0)
      expect(chain.blockedCount).toBe(0)
    })

    it('getChainIds returns only the issue itself', () => {
      const { result } = renderHook(() => useBlockedChain({ edges: [] }))

      const chainIds = result.current.getChainIds('X')
      expect(chainIds.size).toBe(1)
      expect(chainIds.has('X')).toBe(true)
    })
  })

  describe('circular dependencies', () => {
    it('does not infinite loop in getChain', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: circularEdges })
      )

      // Should complete without infinite loop
      const chain = result.current.getChain('H')
      expect(chain.blockers.size).toBeLessThanOrEqual(3)
      expect(chain.blockedBy.size).toBeLessThanOrEqual(3)
    })

    it('does not infinite loop in getChainIds', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: circularEdges })
      )

      // Should complete without infinite loop
      const chainIds = result.current.getChainIds('H')
      expect(chainIds.size).toBeLessThanOrEqual(4) // H + up to 3 others
    })
  })

  describe('enabled option', () => {
    it('returns empty results when enabled is false', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges, enabled: false })
      )

      const chain = result.current.getChain('A')
      expect(chain.blockers.size).toBe(0)
      expect(chain.blockedBy.size).toBe(0)
      expect(chain.blockedCount).toBe(0)
    })

    it('getChainIds returns only self when enabled is false', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges, enabled: false })
      )

      const chainIds = result.current.getChainIds('A')
      expect(chainIds.size).toBe(1)
      expect(chainIds.has('A')).toBe(true)
    })

    it('works correctly when enabled is true', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges, enabled: true })
      )

      const chain = result.current.getChain('A')
      expect(chain.blockedBy.size).toBe(2)
      expect(chain.blockedCount).toBe(2)
    })

    it('defaults to enabled when option not specified', () => {
      const { result } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges })
      )

      const chain = result.current.getChain('A')
      expect(chain.blockedBy.size).toBe(2)
    })
  })

  describe('memoization and stability', () => {
    it('getChain function is stable across re-renders', () => {
      const { result, rerender } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges })
      )

      const getChain1 = result.current.getChain
      rerender()
      const getChain2 = result.current.getChain

      expect(getChain1).toBe(getChain2)
    })

    it('getChainIds function is stable across re-renders', () => {
      const { result, rerender } = renderHook(() =>
        useBlockedChain({ edges: simpleChainEdges })
      )

      const getChainIds1 = result.current.getChainIds
      rerender()
      const getChainIds2 = result.current.getChainIds

      expect(getChainIds1).toBe(getChainIds2)
    })

    it('functions update when edges change', () => {
      const { result, rerender } = renderHook(
        ({ edges }) => useBlockedChain({ edges }),
        { initialProps: { edges: simpleChainEdges } }
      )

      const chain1 = result.current.getChain('A')
      expect(chain1.blockedCount).toBe(2)

      // Change edges
      rerender({ edges: diamondEdges })

      // A is not in diamond edges, so should have 0
      const chain2 = result.current.getChain('A')
      expect(chain2.blockedCount).toBe(0)

      // D is in diamond edges
      const chain3 = result.current.getChain('D')
      expect(chain3.blockedCount).toBe(3)
    })
  })

  describe('real-world scenarios', () => {
    it('handles deep chain (depth limit test)', () => {
      // Create a chain of 25 issues (beyond MAX_DEPTH of 20)
      // N0 -> N1 -> N2 -> ... -> N25
      const deepEdges: DependencyEdge[] = []
      for (let i = 0; i < 25; i++) {
        deepEdges.push(createBlockingEdge(`N${i}`, `N${i + 1}`))
      }

      const { result } = renderHook(() =>
        useBlockedChain({ edges: deepEdges })
      )

      // The chain should be limited by MAX_DEPTH
      const chain = result.current.getChain('N25')
      // Should not have all 25 blockers due to depth limit
      expect(chain.blockers.size).toBeLessThanOrEqual(25)

      // Chain should complete without issues
      const chainIds = result.current.getChainIds('N25')
      expect(chainIds.has('N25')).toBe(true)
    })

    it('handles multiple disconnected subgraphs', () => {
      const disconnectedEdges: DependencyEdge[] = [
        // Subgraph 1: S1A -> S1B -> S1C
        createBlockingEdge('S1A', 'S1B'),
        createBlockingEdge('S1B', 'S1C'),
        // Subgraph 2: S2A -> S2B
        createBlockingEdge('S2A', 'S2B'),
      ]

      const { result } = renderHook(() =>
        useBlockedChain({ edges: disconnectedEdges })
      )

      // S1C should only see S1A and S1B as blockers
      const chain1 = result.current.getChain('S1C')
      expect(chain1.blockers.has('S1A')).toBe(true)
      expect(chain1.blockers.has('S1B')).toBe(true)
      expect(chain1.blockers.has('S2A')).toBe(false)
      expect(chain1.blockers.has('S2B')).toBe(false)

      // S2A should only see S2B as blocked
      const chain2 = result.current.getChain('S2A')
      expect(chain2.blockedBy.has('S2B')).toBe(true)
      expect(chain2.blockedBy.has('S1A')).toBe(false)
    })
  })
})
