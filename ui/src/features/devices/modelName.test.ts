import { describe, expect, it } from "vitest";
import { modelLine, modelName } from "./modelName";

// THE ONE THAT MATTERS. The table is out of date the moment Apple announces something, so an
// identifier it has never heard of has to come back as itself — not blank, and certainly not as
// some other device. A raw identifier on a device card is how quince#836 got reported in the
// first place, which makes this fallback the staleness signal rather than a defect.
describe("modelName fallback", () => {
  it("hands back an identifier it does not know", () => {
    expect(modelName("iPhone19,1")).toBe("iPhone19,1");
    expect(modelName("iPad18,1")).toBe("iPad18,1");
  });

  it("does not invent a name for a device that reported none", () => {
    expect(modelName("")).toBe("");
  });

  // A Record lookup can be fooled by a key inherited from Object.prototype: without a guard,
  // MODELS["constructor"] is a function rather than undefined, and `?? raw` would not catch it.
  it("is not fooled by an inherited property name", () => {
    expect(modelName("constructor")).toBe("constructor");
    expect(modelName("toString")).toBe("toString");
  });
});

// Apple's identifier generation runs ONE AHEAD of the marketing generation, so these two pairs
// look wrong and are right. They are asserted side by side deliberately: anyone who "fixes" the
// table by pattern-matching the number fails here rather than in front of the Operator.
describe("modelName generation offset", () => {
  it("maps iPhone17,* to the iPhone 16 line", () => {
    expect(modelName("iPhone17,1")).toBe("iPhone 16 Pro");
    expect(modelName("iPhone17,2")).toBe("iPhone 16 Pro Max");
  });

  it("maps iPhone18,* to the iPhone 17 line", () => {
    expect(modelName("iPhone18,1")).toBe("iPhone 17 Pro");
    expect(modelName("iPhone18,2")).toBe("iPhone 17 Pro Max");
  });

  // The one device in the iPhone 17 line that does not carry the number.
  it("keeps Apple's name for the one that breaks the pattern", () => {
    expect(modelName("iPhone18,4")).toBe("iPhone Air");
  });
});

// iPad is first-class (design §3), and its numbering is the irregular one: chip-named products,
// and Wi-Fi / cellular variants that Apple markets under a single name.
describe("modelName iPad", () => {
  it("maps both radio variants to one product name", () => {
    expect(modelName("iPad15,7")).toBe("iPad (A16)");
    expect(modelName("iPad15,8")).toBe("iPad (A16)");
  });

  it("maps chip-named products", () => {
    expect(modelName("iPad16,3")).toBe("iPad Pro 11-inch (M4)");
    expect(modelName("iPad14,10")).toBe("iPad Air 13-inch (M2)");
  });
});

describe("modelLine", () => {
  it("joins a known model to its iOS version", () => {
    expect(modelLine("iPhone18,2", "26.6")).toBe("iPhone 17 Pro Max · iOS 26.6");
  });

  // A muxd-minimal device knows neither until qn.3 enrichment lands, and a bare "· iOS" is worse
  // than saying nothing.
  it("drops the parts it does not have", () => {
    expect(modelLine("iPhone18,2", "")).toBe("iPhone 17 Pro Max");
    expect(modelLine("", "26.6")).toBe("iOS 26.6");
    expect(modelLine("", "")).toBe("");
  });
});
