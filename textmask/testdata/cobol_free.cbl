       >>SOURCE FORMAT FREE
IDENTIFICATION DIVISION.
*> free-format comment with 'quote
PROGRAM-ID. HELLO.
PROCEDURE DIVISION.
* not a comment in free format
    DISPLAY 'FREE ''TEXT'''. *> trailing
    STOP RUN.
