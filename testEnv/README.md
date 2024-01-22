
Test environment needs two peers configured for IAX2 Module

**Line 0 peer:**
```
[line0]
type=friend
host=dynamic
regexten=1234
username=line0
secret=line0_123
context=default
auth=md5
permit=0.0.0.0/0.0.0.0
encryption=no
disallow=all
allow=slin
```

**Line 1 peer:**
```
[line1]
type=friend
host=dynamic
regexten=1234
username=line1
secret=line1_123
context=default
auth=md5
permit=0.0.0.0/0.0.0.0
encryption=no
disallow=all
allow=slin
```
