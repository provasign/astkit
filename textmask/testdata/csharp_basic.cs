#region Don't parse this region name
using System;

namespace Demo
{
    /// <summary>Doc comment with 'apostrophe</summary>
    [Description("class Y : Fake")]
    public class X
    {
        string path = @"C:\x\";
        string verb = @"line one
line ""two""";
        string interp = $"{a}";
        string fmt = $"{price:N2} and {{literal}} {Call(1)}";
        string vi = $@"C:\{dir}\file";
        string iv = @$"{dir}\x";
        string raw = """raw "quoted" text""";
        string raw2 = """
            class Phantom { }
            """;
        string raw3 = $$"""{{Name}} and {literal}""";
        char c = '\'';
        char q = '"';
        int a = 1, dir = 2, price = 3;
        string Name = "n";
        int Call(int x) => x;
        string nested = $"{(a > 1 ? "yes" : "no")}";
    }
}
#endregion
