// Package device is the live device table (design §2/§3): it merges N muxer sources (each a
// core/internal/muxd Client) into one presence table keyed by UDID, with per-transport
// presence, and implements httpapi.DeviceReader plus the device.* WS events. Presence is
// muxd-event-driven, never polled — udid + transports + last_seen come from the muxer.
// Lockdown identity (name/model/ios/paired/backup_encryption) is overlaid via Enrich, which
// internal/deviceops (qn.3) drives from ideviceinfo/idevicepair on attach; an undetermined
// field stays at its honest "unknown"/empty default, never guessed.
package device
