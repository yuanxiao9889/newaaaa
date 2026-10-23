package service

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Keep identity tombstones permanently; remove only expired recoverable bodies.
func StartTransferResponseCleanup() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if err := model.CompactTransferResponses(); err != nil {
				common.SysError("transfer response cleanup failed")
			}
			<-ticker.C
		}
	}()
}
