// Apple hardware identifier → marketing name. Device.model is raw (contracts §2); the UI maps
// known ones and falls back to the raw identifier for everything else, so no code is ever
// iPhone-string-specific (design §3: iPhone AND iPad are first-class).
//
// THE DEVICE CANNOT TELL US THIS, so a table is the only option. Measured 2026-08-15 against a
// paired device (quince#836): of the 66 keys lockdown returns in its default domain, plus every
// key in 20 other domains, none carries a human-readable model name. ProductName is "iPhone OS",
// DeviceClass is "iPad", HardwareModel is a board id, and HumanReadableProductVersionString —
// the one that sounds like it — is the iOS version.
//
// ⚠️ APPLE'S IDENTIFIER GENERATION RUNS ONE AHEAD OF THE MARKETING GENERATION. iPhone17,* is the
// iPhone 16 line and iPhone18,* is the iPhone 17 line. The rows below are NOT off by one; anyone
// "correcting" them by pattern-matching the number breaks working mappings. That mistake is what
// quince#836 was filed to prevent.
//
// UNKNOWN IDENTIFIERS FALL BACK TO THE IDENTIFIER, which is what makes a hand-maintained table
// safe to ship. This one goes stale every September, and the failure mode has to stay "shows
// iPhone19,1" rather than "shows the wrong phone" or "shows nothing" — a raw identifier on screen
// is the signal that a row is missing, and it is how quince#836 itself got reported. Adding one
// is one line.
//
// Names are Apple's own, taken from two live sources cross-checked on 2026-08-15. The widely
// copied community lists are not usable verbatim: they carry carrier variants Apple never
// marketed ("iPhone X Global", "iPhone SE (GSM)") and their own iPad generation numbering.
const MODELS: Record<string, string> = {
  // iPhone. Several identifiers per model is normal — they are radio variants.
  "iPhone8,1": "iPhone 6s",
  "iPhone8,2": "iPhone 6s Plus",
  "iPhone8,4": "iPhone SE (1st generation)",
  "iPhone9,1": "iPhone 7",
  "iPhone9,3": "iPhone 7",
  "iPhone9,2": "iPhone 7 Plus",
  "iPhone9,4": "iPhone 7 Plus",
  "iPhone10,1": "iPhone 8",
  "iPhone10,4": "iPhone 8",
  "iPhone10,2": "iPhone 8 Plus",
  "iPhone10,5": "iPhone 8 Plus",
  "iPhone10,3": "iPhone X",
  "iPhone10,6": "iPhone X",
  "iPhone11,2": "iPhone XS",
  "iPhone11,4": "iPhone XS Max",
  "iPhone11,6": "iPhone XS Max",
  "iPhone11,8": "iPhone XR",
  "iPhone12,1": "iPhone 11",
  "iPhone12,3": "iPhone 11 Pro",
  "iPhone12,5": "iPhone 11 Pro Max",
  "iPhone12,8": "iPhone SE (2nd generation)",
  "iPhone13,1": "iPhone 12 mini",
  "iPhone13,2": "iPhone 12",
  "iPhone13,3": "iPhone 12 Pro",
  "iPhone13,4": "iPhone 12 Pro Max",
  "iPhone14,4": "iPhone 13 mini",
  "iPhone14,5": "iPhone 13",
  "iPhone14,2": "iPhone 13 Pro",
  "iPhone14,3": "iPhone 13 Pro Max",
  "iPhone14,6": "iPhone SE (3rd generation)",
  "iPhone14,7": "iPhone 14",
  "iPhone14,8": "iPhone 14 Plus",
  "iPhone15,2": "iPhone 14 Pro",
  "iPhone15,3": "iPhone 14 Pro Max",
  "iPhone15,4": "iPhone 15",
  "iPhone15,5": "iPhone 15 Plus",
  "iPhone16,1": "iPhone 15 Pro",
  "iPhone16,2": "iPhone 15 Pro Max",
  "iPhone17,3": "iPhone 16",
  "iPhone17,4": "iPhone 16 Plus",
  "iPhone17,1": "iPhone 16 Pro",
  "iPhone17,2": "iPhone 16 Pro Max",
  "iPhone17,5": "iPhone 16e",
  "iPhone18,3": "iPhone 17",
  "iPhone18,1": "iPhone 17 Pro",
  "iPhone18,2": "iPhone 17 Pro Max",
  "iPhone18,4": "iPhone Air",
  "iPhone18,5": "iPhone 17e",

  // iPad. The numbering is far less regular than the iPhone's — one generation splits across
  // families, and recent models are named by chip rather than by ordinal. The pairs are Wi-Fi
  // and Wi-Fi + Cellular of the same product, which Apple markets under one name.
  "iPad11,1": "iPad mini (5th generation)",
  "iPad11,2": "iPad mini (5th generation)",
  "iPad11,3": "iPad Air (3rd generation)",
  "iPad11,4": "iPad Air (3rd generation)",
  "iPad11,6": "iPad (8th generation)",
  "iPad11,7": "iPad (8th generation)",
  "iPad12,1": "iPad (9th generation)",
  "iPad12,2": "iPad (9th generation)",
  "iPad13,1": "iPad Air (4th generation)",
  "iPad13,2": "iPad Air (4th generation)",
  "iPad13,4": "iPad Pro 11-inch (3rd generation)",
  "iPad13,5": "iPad Pro 11-inch (3rd generation)",
  "iPad13,6": "iPad Pro 11-inch (3rd generation)",
  "iPad13,7": "iPad Pro 11-inch (3rd generation)",
  "iPad13,8": "iPad Pro 12.9-inch (5th generation)",
  "iPad13,9": "iPad Pro 12.9-inch (5th generation)",
  "iPad13,10": "iPad Pro 12.9-inch (5th generation)",
  "iPad13,11": "iPad Pro 12.9-inch (5th generation)",
  "iPad13,16": "iPad Air (5th generation)",
  "iPad13,17": "iPad Air (5th generation)",
  "iPad13,18": "iPad (10th generation)",
  "iPad13,19": "iPad (10th generation)",
  "iPad14,1": "iPad mini (6th generation)",
  "iPad14,2": "iPad mini (6th generation)",
  "iPad14,3": "iPad Pro 11-inch (4th generation)",
  "iPad14,4": "iPad Pro 11-inch (4th generation)",
  "iPad14,5": "iPad Pro 12.9-inch (6th generation)",
  "iPad14,6": "iPad Pro 12.9-inch (6th generation)",
  "iPad14,8": "iPad Air 11-inch (M2)",
  "iPad14,9": "iPad Air 11-inch (M2)",
  "iPad14,10": "iPad Air 13-inch (M2)",
  "iPad14,11": "iPad Air 13-inch (M2)",
  "iPad15,3": "iPad Air 11-inch (M3)",
  "iPad15,4": "iPad Air 11-inch (M3)",
  "iPad15,5": "iPad Air 13-inch (M3)",
  "iPad15,6": "iPad Air 13-inch (M3)",
  "iPad15,7": "iPad (A16)",
  "iPad15,8": "iPad (A16)",
  "iPad16,1": "iPad mini (A17 Pro)",
  "iPad16,2": "iPad mini (A17 Pro)",
  "iPad16,3": "iPad Pro 11-inch (M4)",
  "iPad16,4": "iPad Pro 11-inch (M4)",
  "iPad16,5": "iPad Pro 13-inch (M4)",
  "iPad16,6": "iPad Pro 13-inch (M4)",
  "iPad16,8": "iPad Air 11-inch (M4)",
  "iPad16,9": "iPad Air 11-inch (M4)",
  "iPad16,10": "iPad Air 13-inch (M4)",
  "iPad16,11": "iPad Air 13-inch (M4)",
  "iPad17,1": "iPad Pro 11-inch (M5)",
  "iPad17,2": "iPad Pro 11-inch (M5)",
  "iPad17,3": "iPad Pro 13-inch (M5)",
  "iPad17,4": "iPad Pro 13-inch (M5)",

  // iPod touch. Long discontinued, still backed up.
  "iPod9,1": "iPod touch (7th generation)",
};

// Object.hasOwn rather than `MODELS[raw] ?? raw`: model comes off the wire, and for the handful of
// strings that name something on Object.prototype ("constructor", "toString") a plain lookup
// returns an inherited function instead of undefined, so `??` never fires and the card renders
// the source of a native function. Not reachable from a real ProductType — but the fallback's
// whole job is to be right about inputs the table has never heard of.
export function modelName(raw: string): string {
  return Object.hasOwn(MODELS, raw) ? MODELS[raw] : raw;
}

// modelLine builds the "iPhone 16 Pro · iOS 26.0.1" subtitle from whatever's known, dropping
// empty parts — so a muxd-minimal device (model/version unknown until qn.3) yields "" rather
// than a bare "· iOS". Shared by the device card and the details header.
export function modelLine(model: string, iosVersion: string): string {
  return [modelName(model), iosVersion ? `iOS ${iosVersion}` : ""].filter(Boolean).join(" · ");
}
