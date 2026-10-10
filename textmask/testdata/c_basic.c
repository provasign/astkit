#include <stdio.h>
#include "local/header.h"
#define GREETING "hello"
#define MULTI(a) \
    do { (a)++; } while (0)

// line comment \
   continued: int phantom(int x);
/* block with 'quote and "dq" */
int main(void) {
    char c = '\'';
    char d = '"';
    const char *s = "esc \" quote // not comment";
    const char *w = L"wide";
    const char *cont = "line \
continued";
    printf("%s %c %c %s %s\n", s, c, d, w, cont);
    return 0;
}
