<?php
namespace App;

// line comment with 'apostrophe
# hash comment
/* block: function phantom() {} */
#[Route('/x')]
class Controller
{
    #[Attr]
    public function act($n)
    {
        $s = 'it\'s #not a comment';
        $d = "r={$this->act(2)} and $n and {$n} and $this->name";
        $arr = "v=$items[0] w=$obj->prop";
        $here = <<<EOT
Heredoc {$this->act(3)} with $n
function phantom2() {}
EOT;
        $now = <<<'NOW'
Nowdoc {$this->act(4)} raw $n
NOW;
        $indented = <<<"IND"
            text $n
            IND;
        $cmd = `ls $dir`;
        return $s . $d . $arr . $here . $now . $indented . $cmd;
    }
}
?>
<html><body>inline HTML don't parse</body></html>
<?php echo "after"; ?>
