#![allow(dead_code)]
//! Crate docs with 'quote
use std::fmt;

/// Docs: don't parse this
#[derive(Debug, Clone)]
struct Holder<'a> {
    name: &'a str,
}

/* outer /* nested */ still comment fn phantom() {} */
fn f<'a>(x: &'a str) -> char {
    '"'
}

fn g() -> &'static str {
    let f = r#"
let client = Bar::new();
"#;
    let h = r##"has "# inside"##;
    let b = b"bytes\"";
    let br = br#"raw "bytes""#;
    let c = c"cstr";
    let ch = 'a';
    let nl = '\n';
    let uni = '\u{1F600}';
    let e = 'é';
    let by = b'x';
    let bq = b'\'';
    let r#type = 1;
    let multi = "line one
line two";
    'outer: loop {
        break 'outer;
    }
    let _ = (f, h, b, br, c, ch, nl, uni, e, by, bq, r#type, multi);
    "done"
}

fn lifetimes<'b, '_>(x: &'b u8) {}

impl<'a> fmt::Display for Holder<'a> {
    fn fmt(&self, w: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(w, "{}", self.name)
    }
}
