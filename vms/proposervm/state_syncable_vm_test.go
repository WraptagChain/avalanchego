// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package proposervm

import (
	"bytes"
	"crypto"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/database/versiondb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/proposervm/state"

	statelessblock "github.com/ava-labs/avalanchego/vms/proposervm/block"
)

type fullVM struct {
	*block.TestVM
	*block.TestHeightIndexedVM
	*block.TestStateSyncableVM
}

func helperBuildStateSyncTestObjects(t *testing.T) (fullVM, *VM) {
	innerVM := fullVM{
		TestVM: &block.TestVM{
			TestVM: common.TestVM{
				T: t,
			},
		},
		TestHeightIndexedVM: &block.TestHeightIndexedVM{
			T: t,
		},
		TestStateSyncableVM: &block.TestStateSyncableVM{
			T: t,
		},
	}

	// Preload DB with key showing height index has been purged of rejected blocks
	dbManager := manager.NewMemDB(version.DefaultVersion1_0_0)
	dbManager = dbManager.NewPrefixDBManager([]byte{})
	rawDB := dbManager.Current().Database
	prefixDB := prefixdb.New(dbPrefix, rawDB)
	vmDB := versiondb.New(prefixDB)
	vmState := state.New(vmDB)
	if err := vmState.SetIndexHasReset(); err != nil {
		t.Fatal("could not preload key to vm state")
	}
	if err := vmDB.Commit(); err != nil {
		t.Fatal("could not commit preloaded key")
	}

	// load innerVM expectations
	innerGenesisBlk := &snowman.TestBlock{
		TestDecidable: choices.TestDecidable{
			IDV: ids.ID{'i', 'n', 'n', 'e', 'r', 'G', 'e', 'n', 's', 'y', 's', 'I', 'D'},
		},
		HeightV: 0,
		BytesV:  []byte("genesis state"),
	}
	innerVM.InitializeF = func(*snow.Context, manager.Manager,
		[]byte, []byte, []byte, chan<- common.Message,
		[]*common.Fx, common.AppSender,
	) error {
		return nil
	}
	innerVM.VerifyHeightIndexF = func() error { return nil }
	innerVM.LastAcceptedF = func() (ids.ID, error) { return innerGenesisBlk.ID(), nil }
	innerVM.GetBlockF = func(i ids.ID) (snowman.Block, error) { return innerGenesisBlk, nil }

	// createVM
	vm := New(innerVM, time.Time{}, uint64(0))

	ctx := snow.DefaultContextTest()
	ctx.NodeID = ids.NodeIDFromCert(pTestCert.Leaf)
	ctx.StakingCertLeaf = pTestCert.Leaf
	ctx.StakingLeafSigner = pTestCert.PrivateKey.(crypto.Signer)

	if err := vm.Initialize(ctx, dbManager, innerGenesisBlk.Bytes(), nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("failed to initialize proposerVM with %s", err)
	}

	return innerVM, vm
}

func TestStateSyncEnabled(t *testing.T) {
	assert := assert.New(t)

	innerVM, vm := helperBuildStateSyncTestObjects(t)

	// ProposerVM State Sync disabled if innerVM State sync is disabled
	vm.hIndexer.MarkRepaired()
	innerVM.StateSyncEnabledF = func() (bool, error) { return false, nil }
	enabled, err := vm.StateSyncEnabled()
	assert.NoError(err)
	assert.False(enabled)

	// ProposerVM State Sync enabled if innerVM State sync is enabled
	innerVM.StateSyncEnabledF = func() (bool, error) { return true, nil }
	enabled, err = vm.StateSyncEnabled()
	assert.NoError(err)
	assert.True(enabled)
}

func TestStateSyncGetOngoingSyncStateSummary(t *testing.T) {
	assert := assert.New(t)

	innerVM, vm := helperBuildStateSyncTestObjects(t)

	innerSummary := &block.TestStateSummary{
		IDV:     ids.ID{'s', 'u', 'm', 'm', 'a', 'r', 'y', 'I', 'D'},
		HeightV: uint64(2022),
		BytesV:  []byte{'i', 'n', 'n', 'e', 'r'},
	}

	// No ongoing state summary case
	innerVM.GetOngoingSyncStateSummaryF = func() (block.StateSummary, error) {
		return nil, database.ErrNotFound
	}
	summary, err := vm.GetOngoingSyncStateSummary()
	assert.True(err == database.ErrNotFound)
	assert.True(summary == nil)

	// Pre fork summary case, fork height not reached hence not set yet
	innerVM.GetOngoingSyncStateSummaryF = func() (block.StateSummary, error) {
		return innerSummary, nil
	}
	_, err = vm.GetForkHeight()
	assert.Equal(err, database.ErrNotFound)
	summary, err = vm.GetOngoingSyncStateSummary()
	assert.NoError(err)
	assert.True(summary.ID() == innerSummary.ID())
	assert.True(summary.Height() == innerSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), innerSummary.Bytes()))

	// Pre fork summary case, fork height already reached
	innerVM.GetOngoingSyncStateSummaryF = func() (block.StateSummary, error) {
		return innerSummary, nil
	}
	assert.NoError(vm.SetForkHeight(innerSummary.Height() + 1))
	summary, err = vm.GetOngoingSyncStateSummary()
	assert.NoError(err)
	assert.True(summary.ID() == innerSummary.ID())
	assert.True(summary.Height() == innerSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), innerSummary.Bytes()))

	// Post fork summary case
	vm.hIndexer.MarkRepaired()
	assert.NoError(vm.SetForkHeight(innerSummary.Height() - 1))

	// store post fork block associated with summary
	innerBlk := &snowman.TestBlock{
		BytesV:     []byte{1},
		TimestampV: vm.Time(),
		HeightV:    innerSummary.Height(),
	}
	innerVM.ParseBlockF = func(b []byte) (snowman.Block, error) {
		assert.True(bytes.Equal(b, innerBlk.Bytes()))
		return innerBlk, nil
	}

	slb, err := statelessblock.Build(
		vm.preferred,
		innerBlk.Timestamp(),
		100, // pChainHeight,
		vm.ctx.StakingCertLeaf,
		innerBlk.Bytes(),
		vm.ctx.ChainID,
		vm.ctx.StakingLeafSigner,
	)
	assert.NoError(err)
	proBlk := &postForkBlock{
		SignedBlock: slb,
		postForkCommonComponents: postForkCommonComponents{
			vm:       vm,
			innerBlk: innerBlk,
			status:   choices.Accepted,
		},
	}
	assert.NoError(vm.storePostForkBlock(proBlk))

	summary, err = vm.GetOngoingSyncStateSummary()
	assert.NoError(err)
	assert.True(summary.Height() == innerSummary.Height())
}

func TestStateSyncGetLastStateSummary(t *testing.T) {
	assert := assert.New(t)

	innerVM, vm := helperBuildStateSyncTestObjects(t)

	innerSummary := &block.TestStateSummary{
		IDV:     ids.ID{'s', 'u', 'm', 'm', 'a', 'r', 'y', 'I', 'D'},
		HeightV: uint64(2022),
		BytesV:  []byte{'i', 'n', 'n', 'e', 'r'},
	}

	// No last state summary case
	innerVM.GetLastStateSummaryF = func() (block.StateSummary, error) {
		return nil, database.ErrNotFound
	}
	summary, err := vm.GetLastStateSummary()
	assert.True(err == database.ErrNotFound)
	assert.True(summary == nil)

	// Pre fork summary case, fork height not reached hence not set yet
	innerVM.GetLastStateSummaryF = func() (block.StateSummary, error) {
		return innerSummary, nil
	}
	_, err = vm.GetForkHeight()
	assert.Equal(err, database.ErrNotFound)
	summary, err = vm.GetLastStateSummary()
	assert.NoError(err)
	assert.True(summary.ID() == innerSummary.ID())
	assert.True(summary.Height() == innerSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), innerSummary.Bytes()))

	// Pre fork summary case, fork height already reached
	innerVM.GetLastStateSummaryF = func() (block.StateSummary, error) {
		return innerSummary, nil
	}
	assert.NoError(vm.SetForkHeight(innerSummary.Height() + 1))
	summary, err = vm.GetLastStateSummary()
	assert.NoError(err)
	assert.True(summary.ID() == innerSummary.ID())
	assert.True(summary.Height() == innerSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), innerSummary.Bytes()))

	// Post fork summary case
	vm.hIndexer.MarkRepaired()
	assert.NoError(vm.SetForkHeight(innerSummary.Height() - 1))

	// store post fork block associated with summary
	innerBlk := &snowman.TestBlock{
		BytesV:     []byte{1},
		TimestampV: vm.Time(),
		HeightV:    innerSummary.Height(),
	}
	innerVM.ParseBlockF = func(b []byte) (snowman.Block, error) {
		assert.True(bytes.Equal(b, innerBlk.Bytes()))
		return innerBlk, nil
	}

	slb, err := statelessblock.Build(
		vm.preferred,
		innerBlk.Timestamp(),
		100, // pChainHeight,
		vm.ctx.StakingCertLeaf,
		innerBlk.Bytes(),
		vm.ctx.ChainID,
		vm.ctx.StakingLeafSigner,
	)
	assert.NoError(err)
	proBlk := &postForkBlock{
		SignedBlock: slb,
		postForkCommonComponents: postForkCommonComponents{
			vm:       vm,
			innerBlk: innerBlk,
			status:   choices.Accepted,
		},
	}
	assert.NoError(vm.storePostForkBlock(proBlk))

	summary, err = vm.GetLastStateSummary()
	assert.NoError(err)
	assert.True(summary.Height() == innerSummary.Height())
}

func TestStateSyncGetStateSummary(t *testing.T) {
	assert := assert.New(t)

	innerVM, vm := helperBuildStateSyncTestObjects(t)
	reqHeight := uint64(1969)

	innerSummary := &block.TestStateSummary{
		IDV:     ids.ID{'s', 'u', 'm', 'm', 'a', 'r', 'y', 'I', 'D'},
		HeightV: reqHeight,
		BytesV:  []byte{'i', 'n', 'n', 'e', 'r'},
	}

	// No state summary case
	innerVM.GetStateSummaryF = func(h uint64) (block.StateSummary, error) {
		return nil, database.ErrNotFound
	}
	summary, err := vm.GetStateSummary(reqHeight)
	assert.True(err == database.ErrNotFound)
	assert.True(summary == nil)

	// Pre fork summary case, fork height not reached hence not set yet
	innerVM.GetStateSummaryF = func(h uint64) (block.StateSummary, error) {
		assert.True(h == reqHeight)
		return innerSummary, nil
	}
	_, err = vm.GetForkHeight()
	assert.Equal(err, database.ErrNotFound)
	summary, err = vm.GetStateSummary(reqHeight)
	assert.NoError(err)
	assert.True(summary.ID() == innerSummary.ID())
	assert.True(summary.Height() == innerSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), innerSummary.Bytes()))

	// Pre fork summary case, fork height already reached
	innerVM.GetStateSummaryF = func(h uint64) (block.StateSummary, error) {
		assert.True(h == reqHeight)
		return innerSummary, nil
	}
	assert.NoError(vm.SetForkHeight(innerSummary.Height() + 1))
	summary, err = vm.GetStateSummary(reqHeight)
	assert.NoError(err)
	assert.True(summary.ID() == innerSummary.ID())
	assert.True(summary.Height() == innerSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), innerSummary.Bytes()))

	// Post fork summary case
	vm.hIndexer.MarkRepaired()
	assert.NoError(vm.SetForkHeight(innerSummary.Height() - 1))

	// store post fork block associated with summary
	innerBlk := &snowman.TestBlock{
		BytesV:     []byte{1},
		TimestampV: vm.Time(),
		HeightV:    innerSummary.Height(),
	}
	innerVM.ParseBlockF = func(b []byte) (snowman.Block, error) {
		assert.True(bytes.Equal(b, innerBlk.Bytes()))
		return innerBlk, nil
	}

	slb, err := statelessblock.Build(
		vm.preferred,
		innerBlk.Timestamp(),
		100, // pChainHeight,
		vm.ctx.StakingCertLeaf,
		innerBlk.Bytes(),
		vm.ctx.ChainID,
		vm.ctx.StakingLeafSigner,
	)
	assert.NoError(err)
	proBlk := &postForkBlock{
		SignedBlock: slb,
		postForkCommonComponents: postForkCommonComponents{
			vm:       vm,
			innerBlk: innerBlk,
			status:   choices.Accepted,
		},
	}
	assert.NoError(vm.storePostForkBlock(proBlk))

	summary, err = vm.GetStateSummary(reqHeight)
	assert.NoError(err)
	assert.True(summary.Height() == innerSummary.Height())
}

func TestParseStateSummary(t *testing.T) {
	assert := assert.New(t)
	innerVM, vm := helperBuildStateSyncTestObjects(t)
	reqHeight := uint64(1969)

	innerSummary := &block.TestStateSummary{
		IDV:     ids.ID{'s', 'u', 'm', 'm', 'a', 'r', 'y', 'I', 'D'},
		HeightV: reqHeight,
		BytesV:  []byte{'i', 'n', 'n', 'e', 'r'},
	}
	innerVM.ParseStateSummaryF = func(summaryBytes []byte) (block.StateSummary, error) {
		assert.True(bytes.Equal(summaryBytes, innerSummary.Bytes()))
		return innerSummary, nil
	}
	innerVM.GetStateSummaryF = func(h uint64) (block.StateSummary, error) {
		assert.True(h == reqHeight)
		return innerSummary, nil
	}

	// Get a pre fork block than parse it
	assert.NoError(vm.SetForkHeight(innerSummary.Height() + 1))
	summary, err := vm.GetStateSummary(reqHeight)
	assert.NoError(err)

	parsedSummary, err := vm.ParseStateSummary(summary.Bytes())
	assert.NoError(err)
	assert.True(summary.ID() == parsedSummary.ID())
	assert.True(summary.Height() == parsedSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), parsedSummary.Bytes()))

	// Get a post fork block than parse it
	vm.hIndexer.MarkRepaired()
	assert.NoError(vm.SetForkHeight(innerSummary.Height() - 1))

	// store post fork block associated with summary
	innerBlk := &snowman.TestBlock{
		BytesV:     []byte{1},
		TimestampV: vm.Time(),
		HeightV:    innerSummary.Height(),
	}
	innerVM.ParseBlockF = func(b []byte) (snowman.Block, error) {
		assert.True(bytes.Equal(b, innerBlk.Bytes()))
		return innerBlk, nil
	}

	slb, err := statelessblock.Build(
		vm.preferred,
		innerBlk.Timestamp(),
		100, // pChainHeight,
		vm.ctx.StakingCertLeaf,
		innerBlk.Bytes(),
		vm.ctx.ChainID,
		vm.ctx.StakingLeafSigner,
	)
	assert.NoError(err)
	proBlk := &postForkBlock{
		SignedBlock: slb,
		postForkCommonComponents: postForkCommonComponents{
			vm:       vm,
			innerBlk: innerBlk,
			status:   choices.Accepted,
		},
	}
	assert.NoError(vm.storePostForkBlock(proBlk))
	assert.NoError(vm.SetForkHeight(innerSummary.Height() - 1))
	summary, err = vm.GetStateSummary(reqHeight)
	assert.NoError(err)

	parsedSummary, err = vm.ParseStateSummary(summary.Bytes())
	assert.NoError(err)
	assert.True(summary.ID() == parsedSummary.ID())
	assert.True(summary.Height() == parsedSummary.Height())
	assert.True(bytes.Equal(summary.Bytes(), parsedSummary.Bytes()))
}

func TestStateSummaryAccept(t *testing.T) {
	assert := assert.New(t)

	innerVM, vm := helperBuildStateSyncTestObjects(t)
	reqHeight := uint64(1969)

	innerSummary := &block.TestStateSummary{
		IDV:     ids.ID{'s', 'u', 'm', 'm', 'a', 'r', 'y', 'I', 'D'},
		HeightV: reqHeight,
		BytesV:  []byte{'i', 'n', 'n', 'e', 'r'},
	}

	vm.hIndexer.MarkRepaired()
	assert.NoError(vm.SetForkHeight(innerSummary.Height() - 1))

	// store post fork block associated with summary
	innerBlk := &snowman.TestBlock{
		BytesV:     []byte{1},
		TimestampV: vm.Time(),
		HeightV:    innerSummary.Height(),
	}
	innerVM.GetStateSummaryF = func(h uint64) (block.StateSummary, error) {
		assert.True(h == reqHeight)
		return innerSummary, nil
	}
	innerVM.ParseBlockF = func(b []byte) (snowman.Block, error) {
		assert.True(bytes.Equal(b, innerBlk.Bytes()))
		return innerBlk, nil
	}

	slb, err := statelessblock.Build(
		vm.preferred,
		innerBlk.Timestamp(),
		100, // pChainHeight,
		vm.ctx.StakingCertLeaf,
		innerBlk.Bytes(),
		vm.ctx.ChainID,
		vm.ctx.StakingLeafSigner,
	)
	assert.NoError(err)
	proBlk := &postForkBlock{
		SignedBlock: slb,
		postForkCommonComponents: postForkCommonComponents{
			vm:       vm,
			innerBlk: innerBlk,
			status:   choices.Accepted,
		},
	}
	assert.NoError(vm.storePostForkBlock(proBlk))

	summary, err := vm.GetStateSummary(reqHeight)
	assert.NoError(err)

	// test Accept accepted
	innerSummary.AcceptF = func() (bool, error) { return true, nil }
	accepted, err := summary.Accept()
	assert.NoError(err)
	assert.True(accepted)

	// test Accept skipped
	innerSummary.AcceptF = func() (bool, error) { return false, nil }
	accepted, err = summary.Accept()
	assert.NoError(err)
	assert.False(accepted)
}
