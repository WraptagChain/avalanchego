// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package syncer

import (
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/validators"
)

// summary content as received from network, along with accumulated weight.
type weightedSummary struct {
	common.Summary
	weight uint64
}

type frontierTracker struct {
	// Holds the beacons that were sampled for the accepted frontier
	frontierSeeders validators.Set
	// IDs of validators we should request state summary frontier from
	targetSeeders ids.ShortSet
	// IDs of validators we requested a state summary frontier from
	// but haven't received a reply yet
	contactedSeeders ids.ShortSet
	// IDs of validators that failed to respond with their state summary frontier
	failedSeeders ids.ShortSet
}

func (ft *frontierTracker) sampleSeeders(fullStateBeaconsList validators.Set, sampleSize int) error {
	// set beacons
	beacons, err := fullStateBeaconsList.Sample(sampleSize)
	if err != nil {
		return err
	}

	ft.frontierSeeders = validators.NewSet()
	if err = ft.frontierSeeders.Set(beacons); err != nil {
		return err
	}

	for _, vdr := range beacons {
		vdrID := vdr.ID()
		ft.targetSeeders.Add(vdrID)
	}

	return nil
}

func (ft *frontierTracker) clear() {
	ft.targetSeeders.Clear()
	ft.contactedSeeders.Clear()
	ft.failedSeeders.Clear()
}

func (ft *frontierTracker) pickSeedersToContact() ids.ShortSet {
	res := ids.NewShortSet(1)
	for ft.targetSeeders.Len() > 0 && res.Len() < maxOutstandingStateSyncRequests {
		vdr, _ := ft.targetSeeders.Pop()
		res.Add(vdr)
	}

	return res
}

func (ft *frontierTracker) hasSeedersToContact() bool {
	return ft.targetSeeders.Len() > 0
}

func (ft *frontierTracker) sampledSeedersWeight() uint64 {
	return ft.frontierSeeders.Weight()
}

func (ft *frontierTracker) markSeederContacted(vdrIDs ids.ShortSet) {
	ft.contactedSeeders.Add(vdrIDs.List()...)
}

func (ft *frontierTracker) hasSeederBeenContacted(vdrID ids.ShortID) bool {
	return ft.contactedSeeders.Contains(vdrID)
}

func (ft *frontierTracker) markSeederResponded(vdrID ids.ShortID) {
	ft.contactedSeeders.Remove(vdrID)
}

func (ft *frontierTracker) anyPendingSeederResponse() bool {
	return ft.contactedSeeders.Len() != 0
}

// Note: markSeederFailed does not remove vdrID from contactedSeeders.
// That is left up to a subsequent markSeederResponded call (with empty summary placeholder)
func (ft *frontierTracker) markSeederFailed(vdrID ids.ShortID) {
	ft.failedSeeders.Add(vdrID)
}

type voteTracker struct {
	// IDs of validators we should request filtering the accepted state summaries from
	targetVoters ids.ShortSet
	// IDs of validators we requested filtering the accepted state summaries from
	// but haven't received a reply yet
	contactedVoters ids.ShortSet
	// IDs of validators that failed to respond with their filtered accepted state summaries
	failedVoters ids.ShortSet
}

func (vt *voteTracker) clear() {
	vt.targetVoters.Clear()
	vt.contactedVoters.Clear()
	vt.failedVoters.Clear()
}

func (vt *voteTracker) storeVoters(vdrs validators.Set) {
	for _, vdr := range vdrs.List() {
		vdrID := vdr.ID()
		vt.targetVoters.Add(vdrID)
	}
}

func (vt *voteTracker) pickVotersToContact() ids.ShortSet {
	res := ids.NewShortSet(1)
	for vt.targetVoters.Len() > 0 && res.Len() < maxOutstandingStateSyncRequests {
		vdr, _ := vt.targetVoters.Pop()
		res.Add(vdr)
	}

	return res
}

func (vt *voteTracker) markVoterContacted(vdrIDs ids.ShortSet) {
	vt.contactedVoters.Add(vdrIDs.List()...)
}

func (vt *voteTracker) hasVoterBeenContacted(vdrID ids.ShortID) bool {
	return vt.contactedVoters.Contains(vdrID)
}

func (vt *voteTracker) markVoterResponded(vdrID ids.ShortID) {
	vt.contactedVoters.Remove(vdrID)
}

func (vt *voteTracker) anyPendingVoterResponse() bool {
	return vt.contactedVoters.Len() != 0
}

func (vt *voteTracker) markVoterFailed(vdrID ids.ShortID) {
	vt.failedVoters.Add(vdrID)
}