package demo

/* outer /* nested */ fun phantom() {} */
// line comment with 'apostrophe
class Greeter(val name: String) {
    fun label(n: Int): String = "n$n"

    fun greet(): String {
        val v = "v=${label(3)} and $name and \"esc\""
        val raw = """
            fun phantom2() {}
            ${label(4)} $name
        """
        val c = '\''
        val q = '"'
        return v + raw + c + q
    }
}
