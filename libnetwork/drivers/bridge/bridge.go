package bridge

import (
	"net"
	"strings"
	"sync"

	"github.com/docker/libnetwork/driverapi"
	"github.com/docker/libnetwork/ipallocator"
	"github.com/docker/libnetwork/netutils"
	"github.com/docker/libnetwork/pkg/options"
	"github.com/docker/libnetwork/portmapper"
	"github.com/docker/libnetwork/sandbox"
	"github.com/docker/libnetwork/types"
	"github.com/vishvananda/netlink"
)

const (
	networkType             = "bridge"
	vethPrefix              = "veth"
	vethLen                 = 7
	containerVeth           = "eth0"
	maxAllocatePortAttempts = 10
)

var (
	ipAllocator *ipallocator.IPAllocator
	portMapper  *portmapper.PortMapper
)

// Configuration info for the "bridge" driver.
type Configuration struct {
	BridgeName            string
	AddressIPv4           *net.IPNet
	FixedCIDR             *net.IPNet
	FixedCIDRv6           *net.IPNet
	EnableIPv6            bool
	EnableIPTables        bool
	EnableIPMasquerade    bool
	EnableICC             bool
	EnableIPForwarding    bool
	AllowNonDefaultBridge bool
	Mtu                   int
	DefaultGatewayIPv4    net.IP
	DefaultGatewayIPv6    net.IP
	DefaultBindingIP      net.IP
}

// EndpointConfiguration represents the user specified configuration for the sandbox endpoint
type EndpointConfiguration struct {
	MacAddress   net.HardwareAddr
	PortBindings []netutils.PortBinding
}

// ContainerConfiguration represents the user specified configuration for a container
type ContainerConfiguration struct {
	parentEndpoints []types.UUID
	childEndpoints  []types.UUID
}

type bridgeEndpoint struct {
	id          types.UUID
	intf        *sandbox.Interface
	config      *EndpointConfiguration // User specified parameters
	portMapping []netutils.PortBinding // Operation port bindings
}

type bridgeNetwork struct {
	id        types.UUID
	bridge    *bridgeInterface               // The bridge's L3 interface
	endpoints map[types.UUID]*bridgeEndpoint // key: endpoint id
	sync.Mutex
}

type driver struct {
	config  *Configuration
	network *bridgeNetwork
	sync.Mutex
}

func init() {
	ipAllocator = ipallocator.New()
	portMapper = portmapper.New()
}

// New provides a new instance of bridge driver
func New() (string, driverapi.Driver) {
	return networkType, &driver{}
}

// Validate performs a static validation on the configuration parameters.
// Whatever can be assessed a priori before attempting any programming.
func (c *Configuration) Validate() error {
	if c.Mtu < 0 {
		return ErrInvalidMtu
	}

	// If bridge v4 subnet is specified
	if c.AddressIPv4 != nil {
		// If Container restricted subnet is specified, it must be a subset of bridge subnet
		if c.FixedCIDR != nil {
			// Check Network address
			if !c.AddressIPv4.Contains(c.FixedCIDR.IP) {
				return ErrInvalidContainerSubnet
			}
			// Check it is effectively a subset
			brNetLen, _ := c.AddressIPv4.Mask.Size()
			cnNetLen, _ := c.FixedCIDR.Mask.Size()
			if brNetLen > cnNetLen {
				return ErrInvalidContainerSubnet
			}
		}
		// If default gw is specified, it must be part of bridge subnet
		if c.DefaultGatewayIPv4 != nil {
			if !c.AddressIPv4.Contains(c.DefaultGatewayIPv4) {
				return ErrInvalidGateway
			}
		}
	}

	// If default v6 gw is specified, FixedCIDRv6 must be specified and gw must belong to FixedCIDRv6 subnet
	if c.EnableIPv6 && c.DefaultGatewayIPv6 != nil {
		if c.FixedCIDRv6 == nil || !c.FixedCIDRv6.Contains(c.DefaultGatewayIPv6) {
			return ErrInvalidGateway
		}
	}

	return nil
}

func (n *bridgeNetwork) getEndpoint(eid types.UUID) (*bridgeEndpoint, error) {
	n.Lock()
	defer n.Unlock()

	if eid == "" {
		return nil, InvalidEndpointIDError(eid)
	}

	if ep, ok := n.endpoints[eid]; ok {
		return ep, nil
	}

	return nil, nil
}

func (d *driver) Config(option map[string]interface{}) error {
	var config *Configuration

	d.Lock()
	defer d.Unlock()

	if d.config != nil {
		return ErrConfigExists
	}

	genericData := option[options.GenericData]
	if genericData != nil {
		switch opt := genericData.(type) {
		case options.Generic:
			opaqueConfig, err := options.GenerateFromModel(opt, &Configuration{})
			if err != nil {
				return err
			}
			config = opaqueConfig.(*Configuration)
		case *Configuration:
			config = opt
		default:
			return ErrInvalidDriverConfig
		}

		if err := config.Validate(); err != nil {
			return err
		}
		d.config = config
	}

	return nil
}

func (d *driver) getNetwork(id types.UUID) (*bridgeNetwork, error) {
	// Just a dummy function to return the only network managed by Bridge driver.
	// But this API makes the caller code unchanged when we move to support multiple networks.
	return d.network, nil
}

// Create a new network using bridge plugin
func (d *driver) CreateNetwork(id types.UUID, option map[string]interface{}) error {
	var err error

	// Driver must be configured
	d.Lock()
	if d.config == nil {
		d.Unlock()
		return ErrInvalidNetworkConfig
	}
	config := d.config

	// Sanity checks
	if d.network != nil {
		d.Unlock()
		return ErrNetworkExists
	}

	// Create and set network handler in driver
	d.network = &bridgeNetwork{id: id, endpoints: make(map[types.UUID]*bridgeEndpoint)}
	d.Unlock()

	// On failure make sure to reset driver network handler to nil
	defer func() {
		if err != nil {
			d.Lock()
			d.network = nil
			d.Unlock()
		}
	}()

	// Create or retrieve the bridge L3 interface
	bridgeIface := newInterface(config)
	d.network.bridge = bridgeIface

	// Prepare the bridge setup configuration
	bridgeSetup := newBridgeSetup(config, bridgeIface)

	// If the bridge interface doesn't exist, we need to start the setup steps
	// by creating a new device and assigning it an IPv4 address.
	bridgeAlreadyExists := bridgeIface.exists()
	if !bridgeAlreadyExists {
		bridgeSetup.queueStep(setupDevice)
		bridgeSetup.queueStep(setupBridgeIPv4)
	}

	// Conditionnally queue setup steps depending on configuration values.
	for _, step := range []struct {
		Condition bool
		Fn        setupStep
	}{
		// Enable IPv6 on the bridge if required. We do this even for a
		// previously  existing bridge, as it may be here from a previous
		// installation where IPv6 wasn't supported yet and needs to be
		// assigned an IPv6 link-local address.
		{config.EnableIPv6, setupBridgeIPv6},

		// We ensure that the bridge has the expectedIPv4 and IPv6 addresses in
		// the case of a previously existing device.
		{bridgeAlreadyExists, setupVerifyAndReconcile},

		// Setup the bridge to allocate containers IPv4 addresses in the
		// specified subnet.
		{config.FixedCIDR != nil, setupFixedCIDRv4},

		// Setup the bridge to allocate containers global IPv6 addresses in the
		// specified subnet.
		{config.FixedCIDRv6 != nil, setupFixedCIDRv6},

		// Setup IPTables.
		{config.EnableIPTables, setupIPTables},

		// Setup IP forwarding.
		{config.EnableIPForwarding, setupIPForwarding},

		// Setup DefaultGatewayIPv4
		{config.DefaultGatewayIPv4 != nil, setupGatewayIPv4},

		// Setup DefaultGatewayIPv6
		{config.DefaultGatewayIPv6 != nil, setupGatewayIPv6},
	} {
		if step.Condition {
			bridgeSetup.queueStep(step.Fn)
		}
	}

	// Apply the prepared list of steps, and abort at the first error.
	bridgeSetup.queueStep(setupDeviceUp)
	if err = bridgeSetup.apply(); err != nil {
		return err
	}

	return nil
}

func (d *driver) DeleteNetwork(nid types.UUID) error {
	var err error

	// Get network handler and remove it from driver
	d.Lock()
	n := d.network
	d.network = nil
	d.Unlock()

	// On failure set network handler back in driver, but
	// only if is not already taken over by some other thread
	defer func() {
		if err != nil {
			d.Lock()
			if d.network == nil {
				d.network = n
			}
			d.Unlock()
		}
	}()

	// Sanity check
	if n == nil {
		err = driverapi.ErrNoNetwork
		return err
	}

	// Cannot remove network if endpoints are still present
	if len(n.endpoints) != 0 {
		err = ActiveEndpointsError(n.id)
		return err
	}

	// Programming
	err = netlink.LinkDel(n.bridge.Link)

	return err
}

func (d *driver) CreateEndpoint(nid, eid types.UUID, epOptions map[string]interface{}) (*sandbox.Info, error) {
	var (
		ipv6Addr *net.IPNet
		err      error
	)

	// Get the network handler and make sure it exists
	d.Lock()
	n := d.network
	config := d.config
	d.Unlock()
	if n == nil {
		return nil, driverapi.ErrNoNetwork
	}

	// Sanity check
	n.Lock()
	if n.id != nid {
		n.Unlock()
		return nil, InvalidNetworkIDError(nid)
	}
	n.Unlock()

	// Check if endpoint id is good and retrieve correspondent endpoint
	ep, err := n.getEndpoint(eid)
	if err != nil {
		return nil, err
	}

	// Endpoint with that id exists either on desired or other sandbox
	if ep != nil {
		return nil, driverapi.ErrEndpointExists
	}

	// Try to convert the options to endpoint configuration
	epConfig, err := parseEndpointOptions(epOptions)
	if err != nil {
		return nil, err
	}

	// Create and add the endpoint
	n.Lock()
	endpoint := &bridgeEndpoint{id: eid, config: epConfig}
	n.endpoints[eid] = endpoint
	n.Unlock()

	// On failure make sure to remove the endpoint
	defer func() {
		if err != nil {
			n.Lock()
			delete(n.endpoints, eid)
			n.Unlock()
		}
	}()

	// Generate a name for what will be the host side pipe interface
	name1, err := generateIfaceName()
	if err != nil {
		return nil, err
	}

	// Generate a name for what will be the sandbox side pipe interface
	name2, err := generateIfaceName()
	if err != nil {
		return nil, err
	}

	// Generate and add the interface pipe host <-> sandbox
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: name1, TxQLen: 0},
		PeerName:  name2}
	if err = netlink.LinkAdd(veth); err != nil {
		return nil, err
	}

	// Get the host side pipe interface handler
	host, err := netlink.LinkByName(name1)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			netlink.LinkDel(host)
		}
	}()

	// Get the sandbox side pipe interface handler
	sbox, err := netlink.LinkByName(name2)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			netlink.LinkDel(sbox)
		}
	}()

	// Set the sbox's MAC. If specified, use the one configured by user, otherwise use a random one
	mac := electMacAddress(epConfig)
	err = netlink.LinkSetHardwareAddr(sbox, mac)
	if err != nil {
		return nil, err
	}

	// Add bridge inherited attributes to pipe interfaces
	if config.Mtu != 0 {
		err = netlink.LinkSetMTU(host, config.Mtu)
		if err != nil {
			return nil, err
		}
		err = netlink.LinkSetMTU(sbox, config.Mtu)
		if err != nil {
			return nil, err
		}
	}

	// Attach host side pipe interface into the bridge
	if err = netlink.LinkSetMaster(host,
		&netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: config.BridgeName}}); err != nil {
		return nil, err
	}

	// v4 address for the sandbox side pipe interface
	ip4, err := ipAllocator.RequestIP(n.bridge.bridgeIPv4, nil)
	if err != nil {
		return nil, err
	}
	ipv4Addr := &net.IPNet{IP: ip4, Mask: n.bridge.bridgeIPv4.Mask}

	// v6 address for the sandbox side pipe interface
	if config.EnableIPv6 {
		var ip6 net.IP

		network := n.bridge.bridgeIPv6
		if config.FixedCIDRv6 != nil {
			network = config.FixedCIDRv6
		}

		ones, _ := network.Mask.Size()
		if ones <= 80 {
			ip6 = make(net.IP, len(network.IP))
			copy(ip6, network.IP)
			for i, h := range mac {
				ip6[i+10] = h
			}
		}

		ip6, err := ipAllocator.RequestIP(network, ip6)
		if err != nil {
			return nil, err
		}

		ipv6Addr = &net.IPNet{IP: ip6, Mask: network.Mask}
	}

	// Create the sandbox side pipe interface
	intf := &sandbox.Interface{}
	intf.SrcName = name2
	intf.DstName = containerVeth
	intf.Address = ipv4Addr

	// Store the interface in endpoint, this is needed for cleanup on DeleteEndpoint()
	endpoint.intf = intf

	// Generate the sandbox info to return
	sinfo := &sandbox.Info{Interfaces: []*sandbox.Interface{intf}}

	// Set the default gateway(s) for the sandbox
	sinfo.Gateway = n.bridge.gatewayIPv4
	if config.EnableIPv6 {
		intf.AddressIPv6 = ipv6Addr
		sinfo.GatewayIPv6 = n.bridge.gatewayIPv6
	}

	// Program any required port mapping and store them in the endpoint
	endpoint.portMapping, err = allocatePorts(epConfig, sinfo, config.DefaultBindingIP)
	if err != nil {
		return nil, err
	}

	return sinfo, nil
}

func (d *driver) DeleteEndpoint(nid, eid types.UUID) error {
	var err error

	// Get the network handler and make sure it exists
	d.Lock()
	n := d.network
	config := d.config
	d.Unlock()
	if n == nil {
		return driverapi.ErrNoNetwork
	}

	// Sanity Check
	n.Lock()
	if n.id != nid {
		n.Unlock()
		return InvalidNetworkIDError(nid)
	}
	n.Unlock()

	// Check endpoint id and if an endpoint is actually there
	ep, err := n.getEndpoint(eid)
	if err != nil {
		return err
	}
	if ep == nil {
		return EndpointNotFoundError(eid)
	}

	// Remove it
	n.Lock()
	delete(n.endpoints, eid)
	n.Unlock()

	// On failure make sure to set back ep in n.endpoints, but only
	// if it hasn't been taken over already by some other thread.
	defer func() {
		if err != nil {
			n.Lock()
			if _, ok := n.endpoints[eid]; !ok {
				n.endpoints[eid] = ep
			}
			n.Unlock()
		}
	}()

	// Remove port mappings. Do not stop endpoint delete on unmap failure
	releasePorts(ep)

	// Release the v4 address allocated to this endpoint's sandbox interface
	err = ipAllocator.ReleaseIP(n.bridge.bridgeIPv4, ep.intf.Address.IP)
	if err != nil {
		return err
	}

	// Release the v6 address allocated to this endpoint's sandbox interface
	if config.EnableIPv6 {
		err := ipAllocator.ReleaseIP(n.bridge.bridgeIPv6, ep.intf.AddressIPv6.IP)
		if err != nil {
			return err
		}
	}

	// Try removal of link. Discard error: link pair might have
	// already been deleted by sandbox delete.
	link, err := netlink.LinkByName(ep.intf.SrcName)
	if err == nil {
		netlink.LinkDel(link)
	}

	return nil
}

// Join method is invoked when a Sandbox is attached to an endpoint.
func (d *driver) Join(nid, eid types.UUID, sboxKey string, options map[string]interface{}) error {
	var err error
	if !d.config.EnableICC {
		err = d.link(nid, eid, options, true)
	}
	return err
}

// Leave method is invoked when a Sandbox detaches from an endpoint.
func (d *driver) Leave(nid, eid types.UUID, options map[string]interface{}) error {
	var err error
	if !d.config.EnableICC {
		err = d.link(nid, eid, options, false)
	}
	return err
}

func (d *driver) link(nid, eid types.UUID, options map[string]interface{}, enable bool) error {
	network, err := d.getNetwork(nid)
	if err != nil {
		return err
	}
	endpoint, err := network.getEndpoint(eid)
	if err != nil {
		return err
	}

	if endpoint == nil {
		return EndpointNotFoundError(eid)
	}

	cc, err := parseContainerOptions(options)
	if err != nil {
		return err
	}
	if cc == nil {
		return nil
	}

	if endpoint.config != nil && endpoint.config.PortBindings != nil {
		for _, p := range cc.parentEndpoints {
			var parentEndpoint *bridgeEndpoint
			parentEndpoint, err = network.getEndpoint(p)
			if err != nil {
				return err
			}
			if parentEndpoint == nil {
				err = InvalidEndpointIDError(string(p))
				return err
			}

			l := newLink(parentEndpoint.intf.Address.IP.String(),
				endpoint.intf.Address.IP.String(),
				endpoint.config.PortBindings, d.config.BridgeName)
			if enable {
				err = l.Enable()
				if err != nil {
					return err
				}
				defer func() {
					if err != nil {
						l.Disable()
					}
				}()
			} else {
				l.Disable()
			}
		}
	}

	for _, c := range cc.childEndpoints {
		var childEndpoint *bridgeEndpoint
		childEndpoint, err = network.getEndpoint(c)
		if err != nil {
			return err
		}
		if childEndpoint == nil {
			err = InvalidEndpointIDError(string(c))
			return err
		}
		if childEndpoint.config == nil || childEndpoint.config.PortBindings == nil {
			continue
		}
		l := newLink(endpoint.intf.Address.IP.String(),
			childEndpoint.intf.Address.IP.String(),
			childEndpoint.config.PortBindings, d.config.BridgeName)
		if enable {
			err = l.Enable()
			if err != nil {
				return err
			}
			defer func() {
				if err != nil {
					l.Disable()
				}
			}()
		} else {
			l.Disable()
		}
	}
	return nil
}

func (d *driver) Type() string {
	return networkType
}

func parseEndpointOptions(epOptions map[string]interface{}) (*EndpointConfiguration, error) {
	if epOptions == nil {
		return nil, nil
	}

	ec := &EndpointConfiguration{}

	if opt, ok := epOptions[options.MacAddress]; ok {
		if mac, ok := opt.(net.HardwareAddr); ok {
			ec.MacAddress = mac
		} else {
			return nil, ErrInvalidEndpointConfig
		}
	}

	if opt, ok := epOptions[options.PortMap]; ok {
		if bs, ok := opt.([]netutils.PortBinding); ok {
			ec.PortBindings = bs
		} else {
			return nil, ErrInvalidEndpointConfig
		}
	}

	return ec, nil
}

func parseContainerOptions(cOptions map[string]interface{}) (*ContainerConfiguration, error) {
	if cOptions == nil {
		return nil, nil
	}
	genericData := cOptions[options.GenericData]
	if genericData == nil {
		return nil, nil
	}
	switch opt := genericData.(type) {
	case options.Generic:
		opaqueConfig, err := options.GenerateFromModel(opt, &ContainerConfiguration{})
		if err != nil {
			return nil, err
		}
		return opaqueConfig.(*ContainerConfiguration), nil
	case *ContainerConfiguration:
		return opt, nil
	default:
		return nil, nil
	}
}

func electMacAddress(epConfig *EndpointConfiguration) net.HardwareAddr {
	if epConfig != nil && epConfig.MacAddress != nil {
		return epConfig.MacAddress
	}
	return netutils.GenerateRandomMAC()
}

// Generates a name to be used for a virtual ethernet
// interface. The name is constructed by 'veth' appended
// by a randomly generated hex value. (example: veth0f60e2c)
func generateIfaceName() (string, error) {
	for i := 0; i < 3; i++ {
		name, err := netutils.GenerateRandomName(vethPrefix, vethLen)
		if err != nil {
			continue
		}
		if _, err := net.InterfaceByName(name); err != nil {
			if strings.Contains(err.Error(), "no such") {
				return name, nil
			}
			return "", err
		}
	}
	return "", ErrIfaceName
}
