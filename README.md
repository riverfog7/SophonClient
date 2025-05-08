## What is this?
This is a port of [collapse launcher's sophon implementation](https://github.com/CollapseLauncher/Hi3Helper.Sophon) in C# to go with a cli.

## How do I build it?
1. Install go and set up your environment.
2. Clone this repository.
   ```bash
   git clone https://github.com/riverfog7/SophonClient.git
   ```
3. Install dependencies.
   ```bash
   go mod tidy
   ```
4. Build the project.
   ```bash
   go build -o sophon-client
    ```
   

## How do I download the game?
1. Find out the `sophon url` of the game you want to download.  
It should look like this: `https://some-endpoint/some/path/to/api/getBuild?some=param&other=param`
2. Run the program with the proper arguments
   ```bash
   ./sophon-client download "https://url/to/api" game "/path/to/game/location"
   ```
3. Wait for the download to finish.
4. Enjoy the game!
