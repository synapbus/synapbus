# Tasks: Trust Scores, Claim Semantics & State-Change Webhooks

## Phase 1: Setup
- [ ] T001 Verify `make test` passes
- [ ] T002 Create migration 014_trust_claims.sql

## Phase 2: Trust Score Backend
- [ ] T003 Create internal/trust/model.go (TrustScore struct, constants, AdjustTrust logic)
- [ ] T004 Create internal/trust/store.go (SQLite CRUD: Get, Upsert, GetAll, AdjustScore)
- [ ] T005 Create internal/trust/service.go (business logic: RecordApproval, RecordRejection, GetScores)
- [ ] T006 [P] Write tests for trust model + store
- [ ] T007 Wire trust service into main.go

## Phase 3: Claim Semantics
- [ ] T008 Add UNIQUE constraint for in_progress claims in reactions store
- [ ] T009 Update reactions service Toggle to check for existing in_progress claims
- [ ] T010 Write tests for claim prevention

## Phase 4: Trust Auto-Adjustment on Reactions
- [ ] T011 Hook trust adjustment into reaction creation (when human reacts to AI message)
- [ ] T012 Add logic to detect human-reacting-to-AI-message pattern
- [ ] T013 Write tests for auto-adjustment

## Phase 5: Webhook State-Change Triggers
- [ ] T014 Add workflow.state_changed event type to dispatcher
- [ ] T015 Fire event from reactions service when state changes
- [ ] T016 Write tests for webhook trigger

## Phase 6: MCP + REST API
- [ ] T017 Register get_trust MCP action in bridge + registry
- [ ] T018 Add GET /api/trust/{agent} REST endpoint
- [ ] T019 Add channel threshold fields (publish_threshold, approve_threshold)

## Phase 7: Polish
- [ ] T020 Run go test ./...
- [ ] T021 Run make build
- [ ] T022 Run make web
