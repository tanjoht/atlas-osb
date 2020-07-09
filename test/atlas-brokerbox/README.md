# atlas-brokerbox

## What is this?

This is a development tool to run a broker instance locally with Docker.
It defines a barebones Docker container to run an instance of the broker.
This broker can read from environment variables or your can mount a local folder with 
your plan templates. By default, it will expect a file called `/keys` and also by default
has *no* creds.

Then the container as a simple broker client tool in the container.

## How to use this tool

0. Setup your environment
```bash
mkdir my-broker-tests && cd my-broker-tests      # call this whatever you want
curl -OL https://raw.githubusercontent.com/jasonmimick/atlas-osb/atlas-brokerbox/test/atlas-brokerbox/run-atlas-brokerbox.sh
```

1. Set up your keys

Create a file called `keys` in your working folder.
This file should looks like this:
```json
{
   "broker": {
      "username": "",
      "password": "",
      "db": "mongodb+srv://"
   },
   "projects": {
      "<prj id>": {
        "publicKey": "",
        "privateKey": "-",
        "id": "mykey",
        "desc": "my project"
      }

   },
   "orgs": {
      "<org_id>": {
        "publicKey": "",
        "privateKey": "",
        "id": "mykey",
        "desc": "testOrg",
        "roles": [
            { "orgId" : "<orgid>" }
        ]
      }
   }
}
```
Setup your plans in a folder called `./plans` in your working folder.

2. Run the broker.

```bash
./run-atlas-brokerbox.sh
```

3. Open a second terminal and get into your working folder. Then you can run a test.

```bash
./test-atlas-broker.sh  --op catalog --verbose
```


## Information on the parts of the tool
run-broker-locally.sh  run.brokerbox.sh
README.md   broker-tester    build.sh          keys       plans          run-broker-tester.sh


## Building and contributing

