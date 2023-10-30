package network

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/swarmbit/spacemesh-state-api/database"
	"github.com/swarmbit/spacemesh-state-api/price"
	"github.com/swarmbit/spacemesh-state-api/types"
)

const INFO_KEY = "info"

type NetworkState struct {
	db             *database.ReadDB
	networkUtils   *NetworkUtils
	networkInfo    *sync.Map
	epochSubsidies *sync.Map
	priceResolver  *price.PriceResolver
}

func NewNetworkState(db *database.ReadDB, networkUtils *NetworkUtils, priceResolver *price.PriceResolver) *NetworkState {
	state := &NetworkState{
		db:             db,
		networkUtils:   networkUtils,
		networkInfo:    &sync.Map{},
		epochSubsidies: &sync.Map{},
		priceResolver:  priceResolver,
	}
	state.fetchNetworkInfo()
	state.periodicNetworkInfoFetch()
	state.calculateEpochSubsidies()
	state.periodicCalculateSubsidy()
	return state
}

func (n *NetworkState) GetInfo() *types.NetworkInfo {
	networkInfo, exists := n.networkInfo.Load(INFO_KEY)
	if !exists {
		return &types.NetworkInfo{}
	}
	return networkInfo.(*types.NetworkInfo)
}

func (n *NetworkState) GetEpochSubsidy(epoch uint32) uint64 {
	subsidy, exists := n.epochSubsidies.Load(epoch)
	if !exists {
		return 0
	}
	return subsidy.(uint64)
}

func (n *NetworkState) periodicNetworkInfoFetch() {
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		for range ticker.C {
			n.fetchNetworkInfo()
		}
	}()
}

func (n *NetworkState) periodicCalculateSubsidy() {
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		for range ticker.C {
			n.calculateEpochSubsidies()
		}
	}()
}

func (n *NetworkState) fetchNetworkInfo() {

	layer, err := n.db.GetLastProcessedLayer()
	if err != nil {
		fmt.Printf("Failed to get last processed layer: %s", err.Error())
		return
	}

	epoch := n.networkUtils.GetEpoch(uint64(layer.Layer))

	atxEpoch, err := n.db.CountAtxEpoch(uint64(epoch - 1))
	if err != nil {
		fmt.Printf("Failed to count atx epoch: %s", err.Error())
		return
	}

	atxNextEpoch, err := n.db.CountAtxEpoch(uint64(epoch))
	if err != nil {
		fmt.Printf("Failed to count next atx epoch: %s", err.Error())
		return
	}

	totalAccounts, err := n.db.CountAccounts()
	if err != nil {
		fmt.Printf("Failed to count accounts: %s", err.Error())
		return
	}

	networkInfo, err := n.db.GetNetworkInfo()
	if err != nil {
		fmt.Printf("Failed to get network info: %s", err.Error())
		return
	}

	atxEpochTotals, err := n.db.GetAtxEpoch(uint64(epoch - 1))
	if err != nil {
		fmt.Printf("Failed to get epoch totals: %s", err.Error())
		return
	}

	atxNextEpochTotals, err := n.db.GetAtxEpoch(uint64(epoch))
	if err != nil {
		fmt.Printf("Failed to get next epoch totals: %s", err.Error())
		return
	}

	hexAtx, err := n.getHigestAtx(uint64(epoch - 1))
	if err != nil {
		fmt.Printf("Failed to get highest atx: %s", err.Error())
		return
	}

	base64Atx, err := hexToBase64(hexAtx)
	if err != nil {
		fmt.Printf("Failed to get base64 atx: %s", err.Error())
		return
	}

	totalSlots, err := n.networkUtils.GetNumberOfSlots(uint64(atxEpochTotals.TotalWeight), atxEpochTotals.TotalWeight, epoch.Uint32())
	if err != nil {
		fmt.Printf("Failed to get total slots: %s", err.Error())
		return
	}

	var genisesAccounts int64 = 28
	var price = n.priceResolver.GetPrice()
	n.networkInfo.Store(INFO_KEY, &types.NetworkInfo{
		Epoch:                  epoch.Uint32(),
		EpochSubsidy:           n.networkUtils.GetEpochSubsidy(uint64(epoch)),
		Layer:                  uint64(layer.Layer),
		TotalSlots:             uint64(totalSlots),
		TotalWeight:            atxEpochTotals.TotalWeight,
		EffectiveUnitsCommited: atxEpochTotals.TotalEffectiveNumUnits,
		CirculatingSupply:      networkInfo.CirculatingSupply,
		Price:                  price,
		MarketCap:              uint64(float64(networkInfo.CirculatingSupply) * price),
		TotalAccounts:          uint64(totalAccounts + genisesAccounts),
		AtxHex:                 hexAtx,
		AtxBase64:              base64Atx,
		TotalActiveSmeshers:    uint64(atxEpoch),
		NextEpoch: &types.NetworkInfoNextEpoch{
			Epoch:                  epoch.Uint32() + 1,
			EffectiveUnitsCommited: int64(atxNextEpochTotals.TotalEffectiveNumUnits),
			TotalActiveSmeshers:    atxNextEpoch,
		},
	})

}

func (n *NetworkState) calculateEpochSubsidies() {
	layer, err := n.db.GetLastProcessedLayer()
	if err != nil {
		fmt.Printf("Failed to get last processed layer: %s", err.Error())
		return
	}

	epoch := n.networkUtils.GetEpoch(uint64(layer.Layer))
	for i := epoch + 1; i >= 2; i-- {
		epochSubsidy := n.networkUtils.GetEpochSubsidy(uint64(i))
		n.epochSubsidies.Store(i.Uint32(), epochSubsidy)
	}
}

func (n *NetworkState) getHigestAtx(epoch uint64) (string, error) {
	atxs, err := n.db.GetAtxForEpoch(epoch)
	if err != nil {
		return "", err
	}

	malfeasanceNodes, err := n.db.GetMalfeasanceNodes()
	if err != nil {
		return "", err
	}

	malfeasanceNodesMap := make(map[string]bool)

	for _, v := range malfeasanceNodes {
		malfeasanceNodesMap[v.ID] = true
	}

	var maxHeight uint64 = 0
	atxID := ""

	for _, atx := range atxs {

		atxHeight := atx.BaseTick + atx.TickCount
		if atxHeight > uint64(maxHeight) && !malfeasanceNodesMap[atx.NodeID] {
			maxHeight = atxHeight
			atxID = atx.AtxID
		}

	}

	return atxID, nil
}

func hexToBase64(hexString string) (string, error) {
	bytes, err := hex.DecodeString(hexString)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}
