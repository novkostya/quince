// Package device is the live device table (design §2/§3): it merges N muxer sources (each a
// core/internal/muxd Client) into one presence table keyed by UDID, with per-transport
// presence, and implements httpapi.DeviceReader plus the device.* WS events. Presence is
// muxd-event-driven, never polled. Identity this rung is muxd-minimal — udid + transports +
// last_seen — with name/model/ios/paired/backup_encryption left at their honest defaults;
// enriching them via lockdown (ideviceinfo/idevicepair) is qn.3's inherited scope.
package device
