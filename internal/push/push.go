package push

import (
	"context"

	pbpush "github.com/KyleYe/open-im-protocol/push"
	"github.com/KyleYe/open-im-server/v3/internal/push/offlinepush"
	"github.com/KyleYe/open-im-server/v3/pkg/common/config"
	"github.com/KyleYe/open-im-server/v3/pkg/common/storage/cache/redis"
	"github.com/KyleYe/open-im-server/v3/pkg/common/storage/controller"
	"github.com/KyleYe/open-im-tools/db/redisutil"
	"github.com/KyleYe/open-im-tools/discovery"
	"google.golang.org/grpc"
)

type pushServer struct {
	database      controller.PushDatabase
	disCov        discovery.SvcDiscoveryRegistry
	offlinePusher offlinepush.OfflinePusher
	pushCh        *ConsumerHandler
}

type Config struct {
	RpcConfig          config.Push
	RedisConfig        config.Redis
	MongodbConfig      config.Mongo
	KafkaConfig        config.Kafka
	NotificationConfig config.Notification
	Share              config.Share
	WebhooksConfig     config.Webhooks
	LocalCacheConfig   config.LocalCache
	Discovery          config.Discovery
	FcmConfigPath      string
}

func (p pushServer) PushMsg(ctx context.Context, req *pbpush.PushMsgReq) (*pbpush.PushMsgResp, error) {
	//todo reserved Interface
	return nil, nil
}

func (p pushServer) DelUserPushToken(ctx context.Context,
	req *pbpush.DelUserPushTokenReq) (resp *pbpush.DelUserPushTokenResp, err error) {
	if err = p.database.DelFcmToken(ctx, req.UserID, int(req.PlatformID)); err != nil {
		return nil, err
	}
	return &pbpush.DelUserPushTokenResp{}, nil
}

func Start(ctx context.Context, config *Config, client discovery.SvcDiscoveryRegistry, server *grpc.Server) error {
	rdb, err := redisutil.NewRedisClient(ctx, config.RedisConfig.Build())
	if err != nil {
		return err
	}
	cacheModel := redis.NewThirdCache(rdb)
	offlinePusher, err := offlinepush.NewOfflinePusher(&config.RpcConfig, cacheModel, config.FcmConfigPath)
	if err != nil {
		return err
	}
	database := controller.NewPushDatabase(cacheModel)

	consumer, err := NewConsumerHandler(config, offlinePusher, rdb, client)
	if err != nil {
		return err
	}
	pbpush.RegisterPushMsgServiceServer(server, &pushServer{
		database:      database,
		disCov:        client,
		offlinePusher: offlinePusher,
		pushCh:        consumer,
	})
	go consumer.pushConsumerGroup.RegisterHandleAndConsumer(ctx, consumer)
	return nil
}
