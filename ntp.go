// Package ntp provides a simple mechanism for querying the current time
// from a remote NTP server.  This package only supports NTP client mode
// behavior and version 4 of the NTP protocol.  See RFC 5905.
// Approach inspired by go-nuts post by Michael Hofmann:
// https://groups.google.com/forum/?fromgroups#!topic/golang-nuts/FlcdMU5fkLQ

package ntp

import (
	"encoding/binary"
	"errors"
	"math"
	"net"
	"time"
)

type mode byte

const (
	reserved mode = 0 + iota
	symmetricActive
	symmetricPassive
	client
	server
	broadcast
	controlMessage
	reservedPrivate
)

type ntpTime struct {
	Seconds  uint32
	Fraction uint32
}

type NtpStats struct {
	Delay  time.Duration
	Offset time.Duration
}

func (t ntpTime) UTC() time.Time {
	nsec := uint64(t.Seconds)*1e9 + (uint64(t.Fraction) * 1e9 >> 32)
	return time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(nsec))
}

func toNtpTime(t time.Time) ntpTime {
	epoch := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	d := t.Sub(epoch)
	i, f := math.Modf(d.Seconds())

	n := new(ntpTime)
	n.Seconds = uint32(i)
	n.Fraction = uint32(f * math.Pow(2, 32)) // fraction * uint32 max value

	return *n
}

type msg struct {
	LiVnMode       byte // Leap Indicator (2) + Version (3) + Mode (3)
	Stratum        byte
	Poll           byte
	Precision      byte
	RootDelay      uint32
	RootDispersion uint32
	ReferenceId    uint32
	ReferenceTime  ntpTime
	OriginTime     ntpTime
	ReceiveTime    ntpTime
	TransmitTime   ntpTime
}

// SetVersion sets the NTP protocol version on the message.
func (m *msg) SetVersion(v byte) {
	m.LiVnMode = (m.LiVnMode & 0xc7) | v<<3
}

// SetMode sets the NTP protocol mode on the message.
func (m *msg) SetMode(md mode) {
	m.LiVnMode = (m.LiVnMode & 0xf8) | byte(md)
}

func (m *msg) SetOriginTime(t ntpTime) {
	m.OriginTime = t
}

func (m *msg) SetTransmitTime(t ntpTime) {
	m.TransmitTime = t
}

// Request returns NTP stats: rtt delay and offset
// from the remote NTP server
// specifed as host.  NTP client mode is used.
func Request(host string) (NtpStats, error) {
	saneEpoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	stats := NtpStats{}
	raddr, err := net.ResolveUDPAddr("udp", host+":123")
	if err != nil {
		return stats, err
	}

	con, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return stats, err
	}
	defer con.Close()
	con.SetDeadline(time.Now().Add(5 * time.Second))

	m := new(msg)
	m.SetMode(client)
	m.SetVersion(4)
	originTime := time.Now() // time client sent request
	m.SetTransmitTime(toNtpTime(originTime))

	err = binary.Write(con, binary.BigEndian, m)
	if err != nil {
		return stats, err
	}

	err = binary.Read(con, binary.BigEndian, m)
	if err != nil {
		return stats, err
	}

	destinationTime := time.Now() // time client got reply

	receiveTime := m.ReceiveTime.UTC()   // time server got request
	transmitTime := m.TransmitTime.UTC() // time server scheduled reply

	if receiveTime.Before(saneEpoch) || transmitTime.Before(saneEpoch) {
		return stats, errors.New("received zero packet")
	}

	// check that server replies to our request
	if m.OriginTime != toNtpTime(originTime) {
		return stats, errors.New("received bogus packet")
	}

	netRttDelay := destinationTime.Sub(originTime)
	srvSchedDelay := transmitTime.Sub(receiveTime)
	delay := netRttDelay - srvSchedDelay

	offset := (receiveTime.Sub(originTime) + transmitTime.Sub(destinationTime)) / 2

	stats = NtpStats{delay, offset}
	return stats, nil

}
