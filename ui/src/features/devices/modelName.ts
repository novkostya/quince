// Partial Apple identifier → marketing name map. Device.model is raw (contracts §2); the
// UI maps known ones and falls back to the raw identifier for everything else, so no code
// is ever iPhone-string-specific (design §3: iPhone AND iPad are first-class).
const MODELS: Record<string, string> = {
  "iPhone17,1": "iPhone 16 Pro",
  "iPhone17,2": "iPhone 16 Pro Max",
  "iPhone16,1": "iPhone 15 Pro",
  "iPhone16,2": "iPhone 15 Pro Max",
  "iPhone15,4": "iPhone 15",
  "iPad13,4": "iPad Pro 11″",
  "iPad14,1": "iPad mini (6th gen)",
};

export function modelName(raw: string): string {
  return MODELS[raw] ?? raw;
}
