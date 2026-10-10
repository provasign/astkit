package x;

import java.util.List;

/**
 * Javadoc: class Phantom { void m() {} }
 */
@SuppressWarnings("unchecked")
public class Basic {
    // line comment with 'apostrophe
    private String tb = """
        He said "hi" and ""
        then \""" escaped
        class Inner {}
        """;
    private char c = '\'';
    private char d = '"';
    private String s = "a \"quoted\" // not comment";
    int n = 1_000_000;

    void run() {
        String odd = """
            one " two "" three \"""
            """;
        System.out.println(odd + s + c + d + tb);
    }
}
