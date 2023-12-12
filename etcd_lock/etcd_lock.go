package etcd_lock

import (
	"context"
	clientV3 "go.etcd.io/etcd/client/v3"
	"k8s.io/klog/v2"
	"log"
	"os"
	"time"
)

// EtcdLocker 分布式锁结构体
type EtcdLocker struct {
	client     *clientV3.Client // 连接到etcd的客户端实例
	lease      clientV3.Lease   // 在etcd上的租约实例
	leaseId    clientV3.LeaseID
	cancelFunc context.CancelFunc
	option     Option
}

// Option EtcdClient的配置选项
type Option struct {
	ConnectionTimeout time.Duration // 连接到etcd的超时时间，示例：5*time.Second
	LeaseTtl          int64         // 租约时长，连接异常断开后，未续租的租约会在这个时间之后失效
	Prefix            string        // 锁前缀
	Username          string        // 用户名，可选
	Password          string        // 密码，可选
	Debug             bool
}

// New 创建一把锁
//
//	etcdEndpoints etcd连接信息，示例：[]string{"localhost:2379"}
//	option 连接选项，包clientV3.Config中的配置项很多，我们其实用不到它们那么多，简化一下
func New(etcdEndpoints []string, option Option) (locker *EtcdLocker, err error) {
	if option.Prefix == "" {
		option.Prefix = "distribution_lock:"
	}
	if option.ConnectionTimeout <= 0 {
		option.ConnectionTimeout = 5 * time.Second
	}
	if option.LeaseTtl <= 0 {
		option.LeaseTtl = 5
	}
	config := clientV3.Config{
		Endpoints:   etcdEndpoints,
		DialTimeout: option.ConnectionTimeout,
		Username:    option.Username,
		Password:    option.Password,
	}
	locker = &EtcdLocker{
		option: option,
	}
	if locker.client, err = clientV3.New(config); err != nil {
		return nil, err
	}
	//defer locker.client.Close()
	var timeoutCtx, cancel = context.Background(), locker.timeoutCancel
	timeoutCtx, cancel = context.WithTimeout(context.Background(), option.ConnectionTimeout)
	defer cancel()
	if _, err := locker.client.Status(timeoutCtx, etcdEndpoints[0]); err != nil {
		return nil, err
	}

	//上锁并创建租约
	locker.lease = clientV3.NewLease(locker.client)
	var leaseGrantResp *clientV3.LeaseGrantResponse
	// 第2个参数TTL，可以用于控制如果当前进程和etcd连接断开了，持有锁的上下文多长时间失效
	if leaseGrantResp, err = locker.lease.Grant(context.TODO(), option.LeaseTtl); err != nil {
		return nil, err
	}
	locker.leaseId = leaseGrantResp.ID
	var ctx context.Context
	// 创建一个可取消的租约，主要是为了退出的时候能够释放
	ctx, locker.cancelFunc = context.WithCancel(context.Background())
	var keepRespChan <-chan *clientV3.LeaseKeepAliveResponse
	if keepRespChan, err = locker.lease.KeepAlive(ctx, locker.leaseId); err != nil {
		return nil, err
	}
	// 续约应答
	go func() {
		for {
			select {
			case keepResp := <-keepRespChan:
				if keepResp == nil {
					if locker.option.Debug {
						log.Printf("进程 %+v 的锁 %+v 的租约已经失效了", os.Getpid(), locker.leaseId)
					}
					return
				} else { // 每秒会续租一次, 所以就会收到一次应答
					if locker.option.Debug {
						log.Printf("进程 %+v 收到自动续租应答 %+v", os.Getpid(), keepResp.ID)
					}
				}
			}
		}
	}()
	return locker, nil
}

func (locker *EtcdLocker) timeoutCancel() {
	if locker.option.Debug {
		log.Printf("进程 %+v 的锁操作撤销", os.Getpid())
	}
}

// GetId 获得当前锁的内部ID
func (locker *EtcdLocker) GetId() int64 {
	return int64(locker.leaseId)
}

// Acquire 获得锁
// lockerId 锁ID，推荐使用UUID或雪花算法，确保唯一性，防止复杂业务+大量数据的情况下发生锁冲撞
// 返回值：who 如果获得锁失败，此ID可以标示锁现在在谁手中（这个谁，来自于GetId()的返回值
//
//	换句话说，A进程获得锁之后，可以通过GetId知道自己的ID是多少，此时B进程获得锁失败，可以通过who返回值知道锁在A手中
func (locker *EtcdLocker) Acquire(lockerId string) (who int64, ok bool) {
	var err error
	// 在租约时间内去抢锁（etcd 里面的锁就是一个 key）
	kv := clientV3.NewKV(locker.client)

	// 创建事务
	txn := kv.Txn(context.TODO())
	// 定义锁的Key
	var lockerKey = locker.option.Prefix + lockerId
	// If 不存在 key，Then 设置它，Else 抢锁失败
	txn.If(clientV3.Compare(clientV3.CreateRevision(lockerKey), "=", 0)).
		Then(clientV3.OpPut(lockerKey, lockerId, clientV3.WithLease(locker.leaseId))).
		Else(clientV3.OpGet(lockerKey))
	var txnResp *clientV3.TxnResponse
	if txnResp, err = txn.Commit(); err != nil {
		return 0, false
	}

	if !txnResp.Succeeded {
		return txnResp.Responses[0].GetResponseRange().Kvs[0].Lease, false
	}
	return 0, true
}

// Release 释放锁
func (locker *EtcdLocker) Release() error {
	locker.cancelFunc()
	klog.Infof("start release locker:%s", locker.leaseId)
	if _, err := locker.lease.Revoke(context.TODO(), locker.leaseId); err != nil {
		return err
	}
	if err := locker.client.Close(); err != nil {
		return err
	}
	klog.Infof("end release locker:%s")
	return nil
}
