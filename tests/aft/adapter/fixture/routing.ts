import type { EvidenceClass } from '@tysonthomas9/aft/types';
import { createFixtureOperationAuthority, type FixtureAuthorityOwner } from '../authority.js';
import type { FixturePlan } from './lifecycle.js';
import { FixtureError } from './lifecycle.js';
const profiles = {
 'agents-real-opencode':{backend:'opencode',observation:'real-native'},
 'agents-emulator':{backend:'opencode',observation:'deterministic'},
 'legacy-deterministic':{backend:null,observation:'deterministic'},
 'legacy-real-codex':{backend:'codex',observation:'real-native'},
 'legacy-real-claude':{backend:'claude',observation:'real-native'},
 'legacy-real-cursor':{backend:'cursor',observation:'real-native'},
 'legacy-real-opencode':{backend:'opencode',observation:'real-native'},
 'legacy-real-codex-podman':{backend:'codex',observation:'real-native'},
} as const;
export function fixtureRouting(plan:FixturePlan) {
 const route=profiles[plan.profile as keyof typeof profiles];if(!route)throw new FixtureError('unsupported-capability');
 if(plan.liveProvider&&(route.backend!==plan.liveProvider.backend||plan.liveProvider.model!==plan.model))throw new FixtureError('identity-mismatch');
 if(route.observation==='deterministic'&&plan.liveProvider)throw new FixtureError('identity-mismatch');
 return Object.freeze({profile:plan.profile,backend:route.backend,model:plan.model,evidenceClass:route.observation as EvidenceClass,externalProvider:!!plan.liveProvider,
   allowedTaskBackends:Object.freeze(route.backend?[route.backend]:['codex','claude','cursor','opencode'])});
}
export function fixtureOperationAuthority(owner:FixtureAuthorityOwner,plan:FixturePlan) {
 const route=fixtureRouting(plan), evidenceClass=route.evidenceClass;
 if(owner.profile!==route.profile)throw new FixtureError('identity-mismatch');
 return createFixtureOperationAuthority(owner,{
  'loom.cli.role':{evidenceClass,effects:['read-api']},'loom.cli.usage':{evidenceClass,effects:['read-api']},
  'loom.fixture.configure':{evidenceClass,effects:['write-fixture']},
  'loom.runtime.stimulate':{evidenceClass,effects:['stop-owned-process','restart-owned-service']},
  ...(route.evidenceClass==='deterministic'?{
    'loom.cli.task':{evidenceClass:'deterministic' as const,effects:['start-owned-process' as const]},
    'loom.fixture.seedWorktree':{evidenceClass:'deterministic' as const,effects:['write-fixture' as const]},
  }:route.externalProvider?{'loom.cli.task':{evidenceClass:'live-provider' as const,effects:['start-owned-process' as const,'external-provider' as const]}}:{}),
 });
}
