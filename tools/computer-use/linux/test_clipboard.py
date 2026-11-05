import sys
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parent))
from clipboard import Clipboard
c=Clipboard()
data={'UTF8_STRING':('UTF8_STRING',8,b'original'), 'text/html':('text/html',8,b'<b>original</b>'), 'application/x-aiclaw-test':('application/x-aiclaw-test',8,b'\x00\x01\xff')}
g=c.generation(); assert c.replace_if_unchanged(data,g)
snapshot,g=c.snapshot(); assert snapshot==data,(snapshot,data)
g=c.replace_if_unchanged({'UTF8_STRING':('UTF8_STRING',8,b'new')},g); assert g
assert c.clipboard.wait_for_text()=='new'
assert c.replace_if_unchanged(snapshot,g,True)
assert c.snapshot()[0]==data
assert c.owns_restored()
assert c.replace_if_unchanged({},(-1,-1)) is None
g=c.generation()
c.clipboard.set_text('newer copy',-1)
assert c.replace_if_unchanged(data,g,True) is None
assert c.clipboard.wait_for_text()=='newer copy'
assert not c.owns_restored()
print('clipboard raw formats, atomic generation check and restore passed',flush=True)
