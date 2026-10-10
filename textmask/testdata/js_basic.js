#!/usr/bin/env node
'use strict';
// line comment with 'apostrophe
/* block: function phantom() {} */
class Widget {
  #priv = 1;
  #tpl;
  constructor(b) {
    this.#tpl = `a ${b} c`;
    this.re = /["']/g;
    this.re2 = /[/]\/x/;
    this.ratio = this.#priv / 2 / 3;
    const nested = `outer ${cond ? `inner ${b + 1}` : 'no'} end`;
    const multi = `line one
function phantom2() {}
line ${b}`;
    const s = 'it\'s' + "say \"hi\"";
    const cont = 'line \
continued';
    if (/^x/.test(s)) {
      return typeof /y/;
    }
  }
}
const obj = { re: /a\/b/i, n: (1) / 2 };
export default Widget;
