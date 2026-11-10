package mw

// hostile carries each log destination's delimiters: field and list separators,
// quotes, backslash, CR/LF, NUL and non-ASCII.
const hostile = "1.2.3.4, x;y=\"z\"\\\r\n{\"forged\":1}\x00é"
