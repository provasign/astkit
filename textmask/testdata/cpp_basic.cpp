#include <vector>
#include <string>

namespace foo {
constexpr int k = 1'000;
}
using namespace foo;
constexpr long big = 1'000'000;
constexpr unsigned hex = 0xFF'FF;

const char *raw = R"(
int phantom(int x) {
)";
const char *raw2 = R"xy(has )" inside)xy";
const char *raw3 = u8R"(utf8 raw)";
auto raw4 = LR"(wide raw)";
auto raw5 = uR"(u16)";
auto raw6 = UR"delim(u32 "q")delim";
const char *u8s = u8"utf8";
char16_t ch = u'x';

class Real {
public:
    int value() const { return k; }  // comment don't
};
