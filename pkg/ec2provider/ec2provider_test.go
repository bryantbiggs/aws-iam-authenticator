package ec2provider

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/aws-iam-authenticator/pkg/metrics"
)

const (
	DescribeDelay = 100
)

type mockDescribeInstancesAPI func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)

func (m mockDescribeInstancesAPI) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m(ctx, params, optFns...)
}

type mockEc2Client struct {
	mockDescribeInstancesAPI
	Reservations []types.Reservation
}

func (c *mockEc2Client) DescribeInstances(in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	// simulate the time it takes for aws to return
	time.Sleep(DescribeDelay * time.Millisecond)
	var reservations []types.Reservation

	for _, res := range c.Reservations {
		var reservation types.Reservation
		for _, inst := range res.Instances {
			for _, id := range in.InstanceIds {
				if id == aws.ToString(inst.InstanceId) {
					reservation.Instances = append(reservation.Instances, inst)
				}
			}
		}
		if len(reservation.Instances) > 0 {
			reservations = append(reservations, reservation)
		}
	}

	return &ec2.DescribeInstancesOutput{
		Reservations: reservations,
	}, nil
}

func newMockedEC2ProviderImpl(t *testing.T) *ec2ProviderImpl {
	dnsCache := ec2PrivateDNSCache{
		cache: make(map[string]string),
		lock:  sync.RWMutex{},
	}
	ec2Requests := ec2Requests{
		set:  make(map[string]bool),
		lock: sync.RWMutex{},
	}
	client := func(t *testing.T) Ec2DescribeInstancesAPI {
		return mockDescribeInstancesAPI(func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {

			return &ec2.DescribeInstancesOutput{}, nil
		})
	}

	return &ec2ProviderImpl{
		ec2:                client(t),
		privateDNSCache:    dnsCache,
		ec2Requests:        ec2Requests,
		instanceIdsChannel: make(chan string, maxChannelSize),
	}
}

func TestGetPrivateDNSName(t *testing.T) {
	metrics.InitMetrics(prometheus.NewRegistry())
	ec2Provider := newMockedEC2ProviderImpl(t)
	reservations := prepare100InstanceOutput()
	ec2Provider.ec2 = &mockEc2Client{Reservations: reservations}
	go ec2Provider.StartEc2DescribeBatchProcessing()
	dns_name, err := ec2Provider.GetPrivateDNSName("ec2-1")
	if err != nil {
		t.Error("There is an error which is not expected when calling ec2 API with setting up mocks")
	}
	if dns_name != "ec2-dns-1" {
		t.Errorf("want: %v, got: %v", "ec2-dns-1", dns_name)
	}
}

func prepareSingleInstanceOutput() []types.Reservation {
	reservations := []types.Reservation{
		{
			Groups: nil,
			Instances: []types.Instance{
				{
					InstanceId:     aws.String("ec2-1"),
					PrivateDnsName: aws.String("ec2-dns-1"),
				},
			},
			OwnerId:       nil,
			RequesterId:   nil,
			ReservationId: nil,
		},
	}

	return reservations
}

func TestGetPrivateDNSNameWithBatching(t *testing.T) {
	metrics.InitMetrics(prometheus.NewRegistry())
	ec2Provider := newMockedEC2ProviderImpl(t)
	reservations := prepare100InstanceOutput()
	ec2Provider.ec2 = &mockEc2Client{Reservations: reservations}
	go ec2Provider.StartEc2DescribeBatchProcessing()
	var wg sync.WaitGroup
	for i := 1; i < 101; i++ {
		instanceString := "ec2-" + strconv.Itoa(i)
		dnsString := "ec2-dns-" + strconv.Itoa(i)
		wg.Add(1)
		// This code helps test the batch functionality twice
		if i == 50 {
			time.Sleep(200 * time.Millisecond)
		}
		go getPrivateDNSName(ec2Provider, instanceString, dnsString, t, &wg)
	}
	wg.Wait()
}

func getPrivateDNSName(ec2provider *ec2ProviderImpl, instanceString string, dnsString string, t *testing.T, wg *sync.WaitGroup) {
	defer wg.Done()
	dnsName, err := ec2provider.GetPrivateDNSName(instanceString)
	if err != nil {
		t.Error("There is an error which is not expected when calling ec2 API with setting up mocks")
	}
	if dnsName != dnsString {
		t.Errorf("want: %v, got: %v", dnsString, dnsName)
	}
}

func prepare100InstanceOutput() []types.Reservation {
	var reservations []types.Reservation

	for i := 1; i < 101; i++ {
		instanceString := "ec2-" + strconv.Itoa(i)
		dnsString := "ec2-dns-" + strconv.Itoa(i)
		instance := types.Instance{
			InstanceId:     aws.String(instanceString),
			PrivateDnsName: aws.String(dnsString),
		}
		var instances []types.Instance
		instances = append(instances, instance)
		res1 := types.Reservation{
			Groups:        nil,
			Instances:     instances,
			OwnerId:       nil,
			RequesterId:   nil,
			ReservationId: nil,
		}
		reservations = append(reservations, res1)
	}

	return reservations
}
