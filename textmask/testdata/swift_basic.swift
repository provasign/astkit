import Foundation

/* outer /* nested */ func phantom() {} */
// line comment with 'apostrophe
struct Greeter {
    let name: String

    func label(_ n: Int) -> String { return "n" }

    func greet() -> String {
        let v = "v=\(label(3)) and \"esc\""
        let raw = #"raw "quoted""#
        let rawHole = #"hole \#(label(4)) and \(notHole)"#
        let multi = """
            func phantom2() {}
            \(label(5))
            """
        let deeper = ##"two "# hashes"##
        return v + raw + rawHole + multi + deeper + name
    }
}
