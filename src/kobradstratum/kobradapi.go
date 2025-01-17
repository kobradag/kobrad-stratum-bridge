package kobradstratum

import (
	"context"
	"fmt"
	"time"

	"github.com/kobradag/kobrad/app/appmessage"
	"github.com/kobradag/kobrad/infrastructure/network/rpcclient"
	"github.com/kobradag/kobrad-stratum-bride/src/gostratum"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type KobraApi struct {
	address       string
	blockWaitTime time.Duration
	logger        *zap.SugaredLogger
	kobrad         *rpcclient.RPCClient
	connected     bool
}

func NewKobraAPI(address string, blockWaitTime time.Duration, logger *zap.SugaredLogger) (*KobraApi, error) {
	client, err := rpcclient.NewRPCClient(address)
	if err != nil {
		return nil, err
	}

	return &KobraApi{
		address:       address,
		blockWaitTime: blockWaitTime,
		logger:        logger.With(zap.String("component", "kobradapi:"+address)),
		kobrad:         client,
		connected:     true,
	}, nil
}

func (kobra *KobraApi) Start(ctx context.Context, blockCb func()) {
	kobra.waitForSync(true)
	go kobra.startBlockTemplateListener(ctx, blockCb)
	go kobra.startStatsThread(ctx)
}

func (kobra *KobraApi) startStatsThread(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			kobra.logger.Warn("context cancelled, stopping stats thread")
			return
		case <-ticker.C:
			dagResponse, err := kobra.kobrad.GetBlockDAGInfo()
			if err != nil {
				kobra.logger.Warn("failed to get network hashrate from kobrad, prom stats will be out of date", zap.Error(err))
				continue
			}
			response, err := kobra.kobrad.EstimateNetworkHashesPerSecond(dagResponse.TipHashes[0], 1000)
			if err != nil {
				kobra.logger.Warn("failed to get network hashrate from kobrad, prom stats will be out of date", zap.Error(err))
				continue
			}
			RecordNetworkStats(response.NetworkHashesPerSecond, dagResponse.BlockCount, dagResponse.Difficulty)
		}
	}
}

func (kobra *KobraApi) reconnect() error {
	if kobra.kobrad != nil {
		return kobra.kobrad.Reconnect()
	}

	client, err := rpcclient.NewRPCClient(kobra.address)
	if err != nil {
		return err
	}
	kobra.kobrad = client
	return nil
}

func (s *KobraApi) waitForSync(verbose bool) error {
	if verbose {
		s.logger.Info("checking kobrad sync state")
	}
	for {
		clientInfo, err := s.kobrad.GetInfo()
		if err != nil {
			return errors.Wrapf(err, "error fetching server info from kobrad @ %s", s.address)
		}
		if clientInfo.IsSynced {
			break
		}
		s.logger.Warn("Kobra is not synced, waiting for sync before starting bridge")
		time.Sleep(5 * time.Second)
	}
	if verbose {
		s.logger.Info("kobrad synced, starting server")
	}
	return nil
}

func (s *KobraApi) startBlockTemplateListener(ctx context.Context, blockReadyCb func()) {
	blockReadyChan := make(chan bool)
	err := s.kobrad.RegisterForNewBlockTemplateNotifications(func(_ *appmessage.NewBlockTemplateNotificationMessage) {
		blockReadyChan <- true
	})
	if err != nil {
		s.logger.Error("fatal: failed to register for block notifications from kobrad")
	}

	ticker := time.NewTicker(s.blockWaitTime)
	for {
		if err := s.waitForSync(false); err != nil {
			s.logger.Error("error checking kobrad sync state, attempting reconnect: ", err)
			if err := s.reconnect(); err != nil {
				s.logger.Error("error reconnecting to kobrad, waiting before retry: ", err)
				time.Sleep(5 * time.Second)
			}
		}
		select {
		case <-ctx.Done():
			s.logger.Warn("context cancelled, stopping block update listener")
			return
		case <-blockReadyChan:
			blockReadyCb()
			ticker.Reset(s.blockWaitTime)
		case <-ticker.C: // timeout, manually check for new blocks
			blockReadyCb()
		}
	}
}

func (kobra *KobraApi) GetBlockTemplate(
	client *gostratum.StratumContext) (*appmessage.GetBlockTemplateResponseMessage, error) {
	template, err := kobra.kobrad.GetBlockTemplate(client.WalletAddr,
		fmt.Sprintf(`'%s' via kobradag/kobrad-stratum-bridge_%s`, client.RemoteApp, version))
	if err != nil {
		return nil, errors.Wrap(err, "failed fetching new block template from kobrad")
	}
	return template, nil
}
