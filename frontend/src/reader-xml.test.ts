import { describe, expect, it } from "vitest";
import { checkXmlComplexity } from "./reader-xml";

describe("preparse XML complexity", () => {
 it("rejects dense or deeply nested input before a DOM can be allocated", () => {
  expect(()=>checkXmlComplexity('<x>'+ '<a/>'.repeat(101)+'</x>',100)).toThrow();
  expect(()=>checkXmlComplexity('<x>'.repeat(101)+'</x>'.repeat(101),1000,100)).toThrow();
 });
 it("charges attribute allocations against the same package-wide budget", () => {
  const attributes = Array.from({length:100},(_,i)=>`a${i}="x"`).join(" ");
  expect(()=>checkXmlComplexity(`<x ${attributes}/>`,100)).toThrow();
  expect(()=>checkXmlComplexity(`<x data=">=\"`,100)).toThrow();
 });
 it("applies a cumulative package budget and rejects unclosed quoted tokens", () => {
  const first=checkXmlComplexity('<x><a/></x>',10);
  expect(()=>checkXmlComplexity('<y><a/><b/></y>',5-first)).toThrow();
  expect(()=>checkXmlComplexity('<x a="unfinished>',10)).toThrow();
 });
});
