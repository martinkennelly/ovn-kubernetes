package node

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/config"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/factory"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/kube"
	ovntest "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/testing"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/util"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func ipEvent(ipStr string, isAdd bool, addrChan chan netlink.AddrUpdate) *net.IPNet {
	ipNet := ovntest.MustParseIPNet(ipStr)
	addrChan <- netlink.AddrUpdate{
		LinkAddress: *ipNet,
		NewAddr:     isAdd,
	}
	return ipNet
}

func nodeHasAddress(fakeClient kubernetes.Interface, nodeName string, ipNet *net.IPNet) bool {
	node, err := fakeClient.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	addrs, err := util.ParseNodeHostCIDRsDropNetMask(node)
	Expect(err).NotTo(HaveOccurred())
	return addrs.Has(ipNet.IP.String())
}

type testCtx struct {
	ipManager    *addressManager
	watchFactory factory.NodeWatchFactory
	fakeClient   kubernetes.Interface
	doneWg       *sync.WaitGroup
	stopCh       chan struct{}
	addrChan     chan netlink.AddrUpdate
	mgmtPortIP4  *net.IPNet
	mgmtPortIP6  *net.IPNet
	subscribed   uint32
}

var _ = Describe("Node IP Handler tests", func() {
	// To ensure that variables don't leak between parallel Ginkgo specs,
	// put all test context into a single struct and reference it via
	// a pointer. The pointer will be different for each spec.
	var tc *testCtx

	const (
		nodeName  string = "node1"
		nodeAddr4 string = "10.1.1.10/24"
		nodeAddr6 string = "2001:db8::10/64"
	)

	BeforeEach(func() {
		useNetlink, createNs, createPrimaryLink := false, false, false
		tc = configureKubeOVNContext(nodeName, useNetlink, createNs, createPrimaryLink)
		// We need to wait until the ipManager's goroutine runs the subscribe
		// function at least once. We can't use a WaitGroup because we have
		// no way to Add(1) to it, and WaitGroups must have matched Add/Done
		// calls.
		subscribe := func() (bool, chan netlink.AddrUpdate, error) {
			defer atomic.StoreUint32(&tc.subscribed, 1)
			tc.addrChan = make(chan netlink.AddrUpdate)
			tc.ipManager.sync()
			return true, tc.addrChan, nil
		}
		tc.doneWg.Add(1)
		go func() {
			tc.ipManager.runInternal(tc.stopCh, subscribe)
			tc.doneWg.Done()
		}()
		Eventually(func() bool {
			return atomic.LoadUint32(&tc.subscribed) == 1
		}, 5).Should(BeTrue())
	})

	AfterEach(func() {
		close(tc.stopCh)
		tc.doneWg.Wait()
		tc.watchFactory.Shutdown()
		close(tc.addrChan)
	})

	Describe("Changing node addresses", func() {
		Context("by adding and deleting a valid IP", func() {
			It("should update node annotations", func() {
				for _, addr := range []string{nodeAddr4, nodeAddr6} {
					ipNet := ipEvent(addr, true, tc.addrChan)
					Eventually(func() bool {
						return nodeHasAddress(tc.fakeClient, nodeName, ipNet)
					}, 5).Should(BeTrue())

					ipNet = ipEvent(addr, false, tc.addrChan)
					Eventually(func() bool {
						return nodeHasAddress(tc.fakeClient, nodeName, ipNet)
					}, 5).Should(BeFalse())
				}
			})
		})

		Context("by adding and deleting an invalid IP", func() {
			It("should not update node annotations", func() {
				for _, addr := range []string{tc.mgmtPortIP4.String(), tc.mgmtPortIP6.String(), config.Gateway.MasqueradeIPs.V4HostMasqueradeIP.String() + "/29", config.Gateway.MasqueradeIPs.V6HostMasqueradeIP.String() + "/125"} {
					ipNet := ipEvent(addr, true, tc.addrChan)
					Consistently(func() bool {
						return nodeHasAddress(tc.fakeClient, nodeName, ipNet)
					}, 3).Should(BeFalse())

					ipNet = ipEvent(addr, false, tc.addrChan)
					Consistently(func() bool {
						return nodeHasAddress(tc.fakeClient, nodeName, ipNet)
					}, 3).Should(BeFalse())
				}
			})
		})
	})

	Describe("Subscription errors", func() {
		It("should resubscribe and continue processing address events", func() {
			// Reset our subscription tracker, close the channel to force
			// the ipManager to resubscribe, and wait until it does
			atomic.StoreUint32(&tc.subscribed, 0)
			close(tc.addrChan)
			Eventually(func() bool {
				return atomic.LoadUint32(&tc.subscribed) == 1
			}, 5).Should(BeTrue())

			ipNet := ipEvent(nodeAddr4, true, tc.addrChan)
			Eventually(func() bool {
				return nodeHasAddress(tc.fakeClient, nodeName, ipNet)
			}, 5).Should(BeTrue())

			ipNet = ipEvent(nodeAddr6, false, tc.addrChan)
			Eventually(func() bool {
				return nodeHasAddress(tc.fakeClient, nodeName, ipNet)
			}, 5).Should(BeFalse())
		})
	})
})

func configureKubeOVNContext(nodeName string, useNetlink, createNs, createLink bool) *testCtx {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Annotations: map[string]string{
				util.OVNNodeHostCIDRs:           `["10.1.1.10/24", "2001:db8::10/64"]`,
				"k8s.ovn.org/l3-gateway-config": `{"default":{"mac-address":"52:54:00:e2:ed:d0","ip-addresses":["10.1.1.10/24"],"ip-address":"10.1.1.10/24","next-hops":["10.1.1.1"],"next-hop":"10.1.1.1"}}`,
			},
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{{Address: "10.1.1.10", Type: corev1.NodeInternalIP}},
		},
	}

	tc := &testCtx{
		doneWg:      &sync.WaitGroup{},
		stopCh:      make(chan struct{}),
		fakeClient:  fake.NewSimpleClientset(node),
		mgmtPortIP4: ovntest.MustParseIPNet("10.1.1.2/24"),
		mgmtPortIP6: ovntest.MustParseIPNet("2001:db8::1/64"),
	}

	var err error
	fakeClientset := &util.OVNNodeClientset{
		KubeClient: tc.fakeClient,
	}
	tc.watchFactory, err = factory.NewNodeWatchFactory(fakeClientset, nodeName)
	Expect(err).NotTo(HaveOccurred())
	err = tc.watchFactory.Start()
	Expect(err).NotTo(HaveOccurred())

	fakeMgmtPortConfig := &managementPortConfig{
		ifName:    nodeName,
		link:      nil,
		routerMAC: nil,
		ipv4: &managementPortIPFamilyConfig{
			ipt:        nil,
			allSubnets: nil,
			ifAddr:     tc.mgmtPortIP4,
			gwIP:       tc.mgmtPortIP4.IP,
		},
		ipv6: &managementPortIPFamilyConfig{
			ipt:        nil,
			allSubnets: nil,
			ifAddr:     tc.mgmtPortIP6,
			gwIP:       tc.mgmtPortIP6.IP,
		},
	}

	fakeBridgeConfiguration := &bridgeConfiguration{bridgeName: "breth0"}

	k := &kube.Kube{KClient: tc.fakeClient}
	if createNs {
		tc.ns, err = testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
	}
	if createLink {
		setupPrimaryInfFn := func() error {
			link := ovntest.AddLink("breth0")
			if err = netlink.AddrAdd(link, &netlink.Addr{IPNet: ovntest.MustParseIPNet("10.1.1.10/24")}); err != nil {
				return err
			}
			return nil
		}
		if createNs {
			Expect(tc.ns.Do(func(netNS ns.NetNS) error {
				return setupPrimaryInfFn()
			}))
		} else {
			Expect(setupPrimaryInfFn()).ShouldNot(HaveOccurred())
		}
	}
	//FIXME (mk): newAddressManagerInternal calls a sync - when this is removed, remove the need to call that func within NS
	if createNs {
		// newAddressManagerInternal calls a sync and this inspects the system, therefore call within namespace
		_ = tc.ns.Do(func(netNS ns.NetNS) error {
			tc.ipManager = newAddressManagerInternal(nodeName, k, fakeMgmtPortConfig, tc.watchFactory, fakeBridgeConfiguration, useNetlink)
			return nil
		})
	} else {
		tc.ipManager = newAddressManagerInternal(nodeName, k, fakeMgmtPortConfig, tc.watchFactory, fakeBridgeConfiguration, useNetlink)
	}
	return tc
}
